package appserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/remotecontrol"
)

const (
	remoteControlTransportWriteTimeout       = 5 * time.Second
	remoteControlTransportStatusPollInterval = 100 * time.Millisecond
)

type RemoteControlTransportServer struct {
	Router             *RuntimeRouter
	Events             <-chan remotecontrol.RemoteClientTransportEvent
	WriteTimeout       time.Duration
	StatusPollInterval time.Duration

	mu               sync.Mutex
	connections      map[remotecontrol.RemoteClientConnectionID]*remoteControlTransportConnection
	requests         sync.WaitGroup
	lastRemoteStatus *remotecontrol.StatusChangedNotification
}

type remoteControlTransportConnection struct {
	remoteID     remotecontrol.RemoteClientConnectionID
	connectionID string
	writer       chan remotecontrol.RemoteClientOutgoingMessage
	disconnect   func()
	initialized  bool
}

func NewRemoteControlTransportServer(router *RuntimeRouter, events <-chan remotecontrol.RemoteClientTransportEvent) *RemoteControlTransportServer {
	return &RemoteControlTransportServer{
		Router: router,
		Events: events,
	}
}

func ServeRemoteControlTransport(ctx context.Context, router *RuntimeRouter, events <-chan remotecontrol.RemoteClientTransportEvent) error {
	return NewRemoteControlTransportServer(router, events).Serve(ctx)
}

func (s *RemoteControlTransportServer) Serve(ctx context.Context) error {
	if s == nil || s.Router == nil {
		return fmt.Errorf("%w: remote control transport router is not configured", ErrInvalidRequest)
	}
	if s.Events == nil {
		return fmt.Errorf("%w: remote control transport event channel is nil", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.ensure()
	s.setLastRemoteControlStatus(s.Router.requireRemote().StatusChanged())
	s.Router.SetNotificationSink(remoteControlNotificationSink{server: s, ctx: ctx})
	s.Router.SetServerRequestSink(remoteControlServerRequestSink{server: s})
	defer s.Router.SetNotificationSink(nil)
	defer s.Router.SetServerRequestSink(nil)

	statusCtx, cancelStatus := context.WithCancel(ctx)
	defer cancelStatus()
	go s.pollRemoteControlStatus(statusCtx)

	for {
		select {
		case <-ctx.Done():
			s.requests.Wait()
			return nil
		case event, ok := <-s.Events:
			if !ok {
				s.requests.Wait()
				return nil
			}
			s.handleEvent(ctx, event)
		}
	}
}

func (s *RemoteControlTransportServer) handleEvent(ctx context.Context, event remotecontrol.RemoteClientTransportEvent) {
	switch event.Type {
	case remotecontrol.RemoteClientConnectionOpened:
		s.openConnection(event)
	case remotecontrol.RemoteClientConnectionClosed:
		s.closeConnection(event.ConnectionID)
	case remotecontrol.RemoteClientIncomingMessage:
		s.handleIncomingMessage(ctx, event)
	}
}

func (s *RemoteControlTransportServer) openConnection(event remotecontrol.RemoteClientTransportEvent) {
	if event.ConnectionID == 0 || event.Writer == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	s.connections[event.ConnectionID] = &remoteControlTransportConnection{
		remoteID:     event.ConnectionID,
		connectionID: remoteControlConnectionIDString(event.ConnectionID),
		writer:       event.Writer,
		disconnect:   event.Disconnect,
	}
}

func (s *RemoteControlTransportServer) closeConnection(remoteID remotecontrol.RemoteClientConnectionID) {
	conn := s.removeConnection(remoteID)
	if conn == nil {
		return
	}
	s.Router.ConnectionClosed(conn.connectionID)
}

func (s *RemoteControlTransportServer) handleIncomingMessage(ctx context.Context, event remotecontrol.RemoteClientTransportEvent) {
	if len(bytes.TrimSpace(event.Message)) == 0 {
		return
	}
	response, request := decodeJSONLine(s.Router, event.Message)
	if request == nil {
		if response != nil {
			s.sendValueToRemoteID(ctx, event.ConnectionID, response)
		}
		return
	}
	conn := s.connection(event.ConnectionID)
	if conn == nil {
		return
	}
	request.ConnectionID = conn.connectionID
	if !conn.initialized && request.Method != MethodInitialize {
		s.sendValueToRemoteID(ctx, event.ConnectionID, ErrorResponse(request.ID, -32600, "Not initialized", nil))
		return
	}
	if request.Method == MethodInitialize {
		response := s.Router.Handle(request)
		if response != nil && response.Error == nil {
			s.markInitialized(event.ConnectionID)
		}
		if response != nil {
			s.sendValueToRemoteID(ctx, event.ConnectionID, response)
			if response.Error == nil {
				if notification := s.Router.initializeRemoteControlStatusNotification(); notification != nil {
					s.sendValueToRemoteID(ctx, event.ConnectionID, notification)
				}
			}
		}
		return
	}
	s.requests.Add(1)
	go func() {
		defer s.requests.Done()
		response := s.Router.Handle(request)
		if response != nil {
			s.sendValueToRemoteID(ctx, event.ConnectionID, response)
		}
	}()
}

func (s *RemoteControlTransportServer) broadcastNotification(ctx context.Context, notification *Notification) {
	if notification == nil {
		return
	}
	if notification.Method == NotificationRemoteControlStatusChanged {
		s.setLastRemoteControlStatus(s.Router.requireRemote().StatusChanged())
	}
	for _, conn := range s.connectionSnapshot(true) {
		if s.connectionOptedOut(conn.connectionID, notification.Method) {
			continue
		}
		s.sendValueToConnection(ctx, conn, notification)
	}
}

func (s *RemoteControlTransportServer) sendNotificationToConnection(ctx context.Context, connectionID string, notification *Notification) {
	if notification == nil {
		return
	}
	for _, conn := range s.connectionSnapshot(false) {
		if conn.connectionID != normalizeConnectionID(connectionID) {
			continue
		}
		if s.connectionOptedOut(conn.connectionID, notification.Method) {
			return
		}
		s.sendValueToConnection(ctx, conn, notification)
		return
	}
}

func (s *RemoteControlTransportServer) pollRemoteControlStatus(ctx context.Context) {
	ticker := time.NewTicker(s.statusPollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status := s.Router.requireRemote().StatusChanged()
			if s.remoteControlStatusUnchanged(status) {
				continue
			}
			s.setLastRemoteControlStatus(status)
			s.broadcastNotification(ctx, NewRemoteControlStatusChangedNotification(status))
		}
	}
}

func (s *RemoteControlTransportServer) sendServerRequestToConnection(ctx context.Context, connectionID string, request *ServerRequest) {
	for _, conn := range s.connectionSnapshot(false) {
		if conn.connectionID != normalizeConnectionID(connectionID) {
			continue
		}
		s.sendValueToConnection(ctx, conn, request)
		return
	}
}

func (s *RemoteControlTransportServer) broadcastServerRequest(ctx context.Context, request *ServerRequest) {
	for _, conn := range s.connectionSnapshot(true) {
		s.sendValueToConnection(ctx, conn, request)
	}
}

func (s *RemoteControlTransportServer) sendValueToRemoteID(ctx context.Context, remoteID remotecontrol.RemoteClientConnectionID, value any) {
	conn := s.connection(remoteID)
	if conn == nil {
		return
	}
	s.sendValueToConnection(ctx, conn, value)
}

func (s *RemoteControlTransportServer) sendValueToConnection(ctx context.Context, conn *remoteControlTransportConnection, value any) {
	if conn == nil || conn.writer == nil || value == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		response, ok := value.(*Response)
		if !ok || response == nil {
			return
		}
		data, err = json.Marshal(ErrorResponse(
			response.ID,
			JSONRPCInternalErrorCode,
			"failed to serialize response: "+err.Error(),
			nil,
		))
		if err != nil {
			return
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	writeCtx, cancel := context.WithTimeout(ctx, s.writeTimeout())
	defer cancel()
	select {
	case conn.writer <- remotecontrol.RemoteClientOutgoingMessage{Message: data}:
	case <-writeCtx.Done():
		if conn.disconnect != nil {
			conn.disconnect()
		}
		s.closeConnection(conn.remoteID)
	}
}

func (s *RemoteControlTransportServer) connection(remoteID remotecontrol.RemoteClientConnectionID) *remoteControlTransportConnection {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	conn := s.connections[remoteID]
	if conn == nil {
		return nil
	}
	copy := *conn
	return &copy
}

func (s *RemoteControlTransportServer) markInitialized(remoteID remotecontrol.RemoteClientConnectionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	if conn := s.connections[remoteID]; conn != nil {
		conn.initialized = true
	}
}

func (s *RemoteControlTransportServer) removeConnection(remoteID remotecontrol.RemoteClientConnectionID) *remoteControlTransportConnection {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	conn := s.connections[remoteID]
	delete(s.connections, remoteID)
	if conn == nil {
		return nil
	}
	copy := *conn
	return &copy
}

func (s *RemoteControlTransportServer) connectionSnapshot(initializedOnly bool) []*remoteControlTransportConnection {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
	connections := make([]*remoteControlTransportConnection, 0, len(s.connections))
	for _, conn := range s.connections {
		if initializedOnly && !conn.initialized {
			continue
		}
		copy := *conn
		connections = append(connections, &copy)
	}
	return connections
}

func (s *RemoteControlTransportServer) connectionOptedOut(connectionID string, method NotificationMethod) bool {
	if s == nil || s.Router == nil || strings.TrimSpace(string(method)) == "" {
		return false
	}
	connectionID = normalizeConnectionID(connectionID)
	s.Router.clientInfoMu.RLock()
	defer s.Router.clientInfoMu.RUnlock()
	methods := s.Router.notificationOptOut[connectionID]
	if len(methods) == 0 {
		return false
	}
	_, ok := methods[method]
	return ok
}

func (s *RemoteControlTransportServer) remoteControlStatusUnchanged(status *remotecontrol.StatusChangedNotification) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return remoteControlStatusChangedNotificationEqual(s.lastRemoteStatus, status)
}

func (s *RemoteControlTransportServer) setLastRemoteControlStatus(status *remotecontrol.StatusChangedNotification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastRemoteStatus = cloneRemoteControlStatusChangedNotification(status)
}

func (s *RemoteControlTransportServer) ensure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLocked()
}

func (s *RemoteControlTransportServer) ensureLocked() {
	if s.connections == nil {
		s.connections = map[remotecontrol.RemoteClientConnectionID]*remoteControlTransportConnection{}
	}
}

func (s *RemoteControlTransportServer) writeTimeout() time.Duration {
	if s != nil && s.WriteTimeout > 0 {
		return s.WriteTimeout
	}
	return remoteControlTransportWriteTimeout
}

func (s *RemoteControlTransportServer) statusPollInterval() time.Duration {
	if s != nil && s.StatusPollInterval > 0 {
		return s.StatusPollInterval
	}
	return remoteControlTransportStatusPollInterval
}

type remoteControlServerRequestSink struct {
	server *RemoteControlTransportServer
}

func (s remoteControlServerRequestSink) SendServerRequest(request *ServerRequest) {
	if s.server != nil {
		s.server.broadcastServerRequest(context.Background(), request)
	}
}

func (s remoteControlServerRequestSink) SendServerRequestToConnection(connectionID string, request *ServerRequest) {
	if s.server != nil {
		s.server.sendServerRequestToConnection(context.Background(), connectionID, request)
	}
}

type remoteControlNotificationSink struct {
	server *RemoteControlTransportServer
	ctx    context.Context
}

func (s remoteControlNotificationSink) Notify(notification *Notification) {
	if s.server != nil {
		s.server.broadcastNotification(s.ctx, notification)
	}
}

func (s remoteControlNotificationSink) NotifyToConnection(connectionID string, notification *Notification) {
	if s.server != nil {
		s.server.sendNotificationToConnection(s.ctx, connectionID, notification)
	}
}

func remoteControlConnectionIDString(id remotecontrol.RemoteClientConnectionID) string {
	return fmt.Sprintf("remote-control-%d", id)
}

func cloneRemoteControlStatusChangedNotification(status *remotecontrol.StatusChangedNotification) *remotecontrol.StatusChangedNotification {
	if status == nil {
		return nil
	}
	clone := *status
	if status.EnvironmentID != nil {
		value := *status.EnvironmentID
		clone.EnvironmentID = &value
	}
	return &clone
}

func remoteControlStatusChangedNotificationEqual(left *remotecontrol.StatusChangedNotification, right *remotecontrol.StatusChangedNotification) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Status == right.Status &&
		left.ServerName == right.ServerName &&
		left.InstallationID == right.InstallationID &&
		remoteControlStringPtrEqual(left.EnvironmentID, right.EnvironmentID)
}

func remoteControlStringPtrEqual(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
