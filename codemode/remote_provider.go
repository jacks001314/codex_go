package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"codex_go/tool"
)

type WebSocketProvider struct {
	url        string
	httpClient *http.Client

	mu          sync.Mutex
	connection  *remoteConnection
	nextSession atomic.Uint64
}

type remoteSessionProvider interface {
	connect(context.Context) (*remoteConnection, error)
}

type remoteTransport interface {
	Read(context.Context, any) (bool, error)
	Write(context.Context, any) error
	Close() error
}

func NewWebSocketProvider(url string, httpClient *http.Client) *WebSocketProvider {
	return &WebSocketProvider{url: url, httpClient: httpClient}
}

func (p *WebSocketProvider) Availability() error { return nil }

func (p *WebSocketProvider) TakeUnavailableWarning(string) string { return "" }

func (p *WebSocketProvider) NewSession(delegate tool.CodeModeRemoteDelegate) tool.CodeModeRemoteSession {
	value := p.nextSession.Add(1)
	return &remoteSession{provider: p, delegate: delegate, id: SessionID(fmt.Sprintf("session-%d", value))}
}

func (p *WebSocketProvider) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	connection := p.connection
	p.connection = nil
	p.mu.Unlock()
	if connection != nil {
		return connection.Close()
	}
	return nil
}

func (p *WebSocketProvider) connect(ctx context.Context) (*remoteConnection, error) {
	if p == nil {
		return nil, fmt.Errorf("code-mode websocket provider is nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.connection != nil && p.connection.Alive() {
		return p.connection, nil
	}
	transport, _, err := DialWebSocket(ctx, p.url, p.httpClient)
	if err != nil {
		return nil, err
	}
	connection, err := connectRemoteTransport(ctx, transport)
	if err != nil {
		return nil, err
	}
	p.connection = connection
	return connection, nil
}

func connectRemoteTransport(ctx context.Context, transport remoteTransport) (*remoteConnection, error) {
	versions, _ := NewSupportedProtocolVersions(ProtocolV1)
	hello, _ := NewClientHello(versions, CapabilitySet{}, CapabilitySet{})
	if err := transport.Write(ctx, ClientHelloMessage(hello)); err != nil {
		_ = transport.Close()
		return nil, err
	}
	var response HostToClient
	ok, err := transport.Read(ctx, &response)
	if err != nil {
		_ = transport.Close()
		return nil, err
	}
	if !ok {
		_ = transport.Close()
		return nil, fmt.Errorf("code-mode host closed during handshake")
	}
	if response.Type == "connection/rejected" {
		_ = transport.Close()
		return nil, fmt.Errorf("code-mode host rejected handshake: %s", handshakeRejectMessage(response.Reason))
	}
	if response.Type != "connection/ready" || response.Hello == nil || response.Hello.SelectedVersion != ProtocolV1 {
		_ = transport.Close()
		return nil, fmt.Errorf("code-mode host returned an invalid handshake response")
	}
	connection := newRemoteConnection(transport)
	go connection.readLoop()
	return connection, nil
}

func handshakeRejectMessage(reason *HandshakeRejectReason) string {
	if reason == nil {
		return "unknown reason"
	}
	if reason.Message != "" {
		return reason.Message
	}
	return reason.Type
}

type remoteSession struct {
	provider remoteSessionProvider
	delegate tool.CodeModeRemoteDelegate
	id       SessionID

	openMu   sync.Mutex
	mu       sync.Mutex
	openedOn *remoteConnection
	closed   bool
}

func (s *remoteSession) Execute(ctx context.Context, request tool.CodeModeRemoteExecuteRequest) (tool.CodeModeRemoteResponse, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return tool.CodeModeRemoteResponse{}, err
	}
	definitions := make([]ProtocolToolDefinition, 0, len(request.EnabledTools))
	for _, definition := range request.EnabledTools {
		kind := ProtocolToolKindFunction
		if definition.Kind == tool.PayloadCustom {
			kind = ProtocolToolKindFreeform
		}
		var namespace *string
		if definition.ToolName.Namespace != "" {
			value := definition.ToolName.Namespace
			namespace = &value
		}
		definitions = append(definitions, ProtocolToolDefinition{
			Name:         definition.Name,
			ToolName:     ToolName{Name: definition.ToolName.Name, Namespace: namespace},
			Description:  definition.Description,
			Kind:         kind,
			InputSchema:  append(json.RawMessage(nil), definition.InputSchema...),
			OutputSchema: append(json.RawMessage(nil), definition.OutputSchema...),
		})
	}
	response, err := connection.Execute(ctx, s.id, s.delegate, ExecuteRequest{
		ToolCallID: request.ToolCallID, EnabledTools: definitions, Source: request.Source,
		YieldTimeMS: request.YieldTimeMS, MaxOutputTokens: request.MaxOutputTokens,
	})
	return publicRemoteResponse(response), err
}

func (s *remoteSession) Wait(ctx context.Context, cellID string, yieldTimeMS uint64) (tool.CodeModeRemoteResponse, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return tool.CodeModeRemoteResponse{}, err
	}
	outcome, err := connection.Request(ctx, WaitSessionRequest(s.id, WaitRequest{CellID: CellID(cellID), YieldTimeMS: yieldTimeMS}))
	if err != nil {
		return tool.CodeModeRemoteResponse{}, err
	}
	if outcome.Outcome == nil {
		return tool.CodeModeRemoteResponse{}, fmt.Errorf("code-mode host returned an invalid wait response")
	}
	return publicRemoteResponse(outcome.Outcome.Response), nil
}

func (s *remoteSession) Terminate(ctx context.Context, cellID string) (tool.CodeModeRemoteResponse, error) {
	connection, err := s.connection(ctx)
	if err != nil {
		return tool.CodeModeRemoteResponse{}, err
	}
	outcome, err := connection.Request(ctx, TerminateSessionRequest(s.id, CellID(cellID)))
	if err != nil {
		return tool.CodeModeRemoteResponse{}, err
	}
	if outcome.Outcome == nil {
		return tool.CodeModeRemoteResponse{}, fmt.Errorf("code-mode host returned an invalid terminate response")
	}
	return publicRemoteResponse(outcome.Outcome.Response), nil
}

func (s *remoteSession) Close() error {
	if s == nil {
		return nil
	}
	s.openMu.Lock()
	defer s.openMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	connection := s.openedOn
	s.openedOn = nil
	s.mu.Unlock()
	if connection == nil {
		return nil
	}
	connection.RemoveDelegate(s.id)
	ctx, cancel := context.WithTimeout(context.Background(), WebSocketCloseTimeout)
	defer cancel()
	_, err := connection.Request(ctx, ShutdownSessionRequest(s.id))
	return err
}

func (s *remoteSession) connection(ctx context.Context) (*remoteConnection, error) {
	if s == nil || s.provider == nil {
		return nil, fmt.Errorf("code-mode remote session is nil")
	}
	s.openMu.Lock()
	defer s.openMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("code mode session is shutting down")
	}
	s.mu.Unlock()
	connection, err := s.provider.connect(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("code mode session is shutting down")
	}
	if s.openedOn == connection {
		s.mu.Unlock()
		return connection, nil
	}
	s.mu.Unlock()
	response, err := connection.Request(ctx, OpenSessionRequest(s.id))
	if err != nil {
		return nil, err
	}
	if response.Type != "session/ready" || response.SessionID != s.id {
		return nil, fmt.Errorf("code-mode host returned an invalid open-session response")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("code mode session is shutting down")
	}
	s.openedOn = connection
	s.mu.Unlock()
	connection.SetDelegate(s.id, s.delegate)
	return connection, nil
}

func publicRemoteResponse(response RuntimeResponse) tool.CodeModeRemoteResponse {
	state := "completed"
	if response.Variant == "Yielded" {
		state = "yielded"
	} else if response.Variant == "Terminated" {
		state = "terminated"
	}
	items := make([]map[string]any, 0, len(response.ContentItems))
	for _, item := range response.ContentItems {
		value := map[string]any{"type": item.Type}
		if item.Text != "" {
			value["text"] = item.Text
		}
		if item.ImageURL != "" {
			value["image_url"] = item.ImageURL
			if item.Detail != nil {
				value["detail"] = string(*item.Detail)
			}
		}
		if item.AudioURL != "" {
			value["audio_url"] = item.AudioURL
		}
		items = append(items, value)
	}
	errorText := ""
	if response.ErrorText != nil {
		errorText = *response.ErrorText
	}
	return tool.CodeModeRemoteResponse{CellID: response.CellID.String(), State: state, ContentItems: items, ErrorText: errorText}
}

type remotePending struct {
	response chan remoteResult
	initial  chan remoteInitialResult
}

type remoteResult struct {
	response HostResponse
	err      error
}

type remoteInitialResult struct {
	response RuntimeResponse
	err      error
}

type remoteConnection struct {
	transport remoteTransport
	writeMu   sync.Mutex
	mu        sync.Mutex
	alive     bool
	nextID    RequestID
	pending   map[RequestID]*remotePending
	delegates map[SessionID]tool.CodeModeRemoteDelegate
	cancels   map[DelegateRequestID]context.CancelFunc
}

func newRemoteConnection(transport remoteTransport) *remoteConnection {
	return &remoteConnection{transport: transport, alive: true, nextID: 1, pending: map[RequestID]*remotePending{}, delegates: map[SessionID]tool.CodeModeRemoteDelegate{}, cancels: map[DelegateRequestID]context.CancelFunc{}}
}

func (c *remoteConnection) Alive() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.alive
}

func (c *remoteConnection) SetDelegate(sessionID SessionID, delegate tool.CodeModeRemoteDelegate) {
	c.mu.Lock()
	if delegate != nil {
		c.delegates[sessionID] = delegate
	}
	c.mu.Unlock()
}

func (c *remoteConnection) RemoveDelegate(sessionID SessionID) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.delegates, sessionID)
	c.mu.Unlock()
}

func (c *remoteConnection) Request(ctx context.Context, request HostRequest) (HostResponse, error) {
	pending, id, err := c.sendRequest(ctx, request, false)
	if err != nil {
		return HostResponse{}, err
	}
	select {
	case result := <-pending.response:
		return result.response, result.err
	case <-ctx.Done():
		c.cancelRequest(id)
		return HostResponse{}, ctx.Err()
	}
}

func (c *remoteConnection) Execute(ctx context.Context, sessionID SessionID, delegate tool.CodeModeRemoteDelegate, request ExecuteRequest) (RuntimeResponse, error) {
	c.SetDelegate(sessionID, delegate)
	pending, id, err := c.sendRequest(ctx, ExecuteSessionRequest(sessionID, request), true)
	if err != nil {
		return RuntimeResponse{}, err
	}
	select {
	case result := <-pending.response:
		if result.err != nil {
			return RuntimeResponse{}, result.err
		}
		if result.response.Type != "execution/started" {
			return RuntimeResponse{}, fmt.Errorf("code-mode host returned an invalid execute response")
		}
	case <-ctx.Done():
		c.cancelRequest(id)
		return RuntimeResponse{}, ctx.Err()
	}
	select {
	case result := <-pending.initial:
		return result.response, result.err
	case <-ctx.Done():
		c.cancelRequest(id)
		return RuntimeResponse{}, ctx.Err()
	}
}

func (c *remoteConnection) sendRequest(ctx context.Context, request HostRequest, initial bool) (*remotePending, RequestID, error) {
	if c == nil {
		return nil, 0, fmt.Errorf("code-mode remote connection is nil")
	}
	c.mu.Lock()
	if !c.alive {
		c.mu.Unlock()
		return nil, 0, fmt.Errorf("code-mode host connection closed")
	}
	id := c.nextID
	c.nextID++
	pending := &remotePending{response: make(chan remoteResult, 1)}
	if initial {
		pending.initial = make(chan remoteInitialResult, 1)
	}
	c.pending[id] = pending
	c.mu.Unlock()
	if err := c.write(ctx, OperationRequest(id, request)); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, 0, err
	}
	return pending, id, nil
}

func (c *remoteConnection) cancelRequest(id RequestID) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
	_ = c.write(context.Background(), CancelRequest(id))
}

func (c *remoteConnection) write(ctx context.Context, message any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.transport.Write(ctx, message)
}

func (c *remoteConnection) readLoop() {
	for {
		var message HostToClient
		ok, err := c.transport.Read(context.Background(), &message)
		if err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("code-mode host connection closed")
			}
			c.fail(err)
			return
		}
		c.handle(message)
	}
}

func (c *remoteConnection) handle(message HostToClient) {
	switch message.Type {
	case "operation/response":
		c.mu.Lock()
		pending := c.pending[message.ID]
		if pending != nil && pending.initial == nil {
			delete(c.pending, message.ID)
		}
		c.mu.Unlock()
		if pending == nil {
			c.fail(fmt.Errorf("code-mode host returned unknown request ID %d", message.ID))
			return
		}
		response, err := message.Result.IntoResult()
		pending.response <- remoteResult{response: response, err: err}
	case "execute/initialResponse":
		c.mu.Lock()
		pending := c.pending[message.ID]
		delete(c.pending, message.ID)
		c.mu.Unlock()
		if pending == nil || pending.initial == nil {
			c.fail(fmt.Errorf("code-mode host returned initial response for unknown request ID %d", message.ID))
			return
		}
		response, err := message.Initial.IntoResult()
		pending.initial <- remoteInitialResult{response: response, err: err}
	case "delegate/request":
		go c.handleDelegate(message)
	case "delegate/cancel":
		c.mu.Lock()
		cancel := c.cancels[message.DelegateID]
		delete(c.cancels, message.DelegateID)
		c.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	case "cell/closed":
		// The public executor removes cells after terminal wait responses.
	case "connection/ready", "connection/rejected":
		c.fail(fmt.Errorf("code-mode host sent a second handshake response"))
	}
}

func (c *remoteConnection) handleDelegate(message HostToClient) {
	c.mu.Lock()
	delegate := c.delegates[message.SessionID]
	c.mu.Unlock()
	ctx, cancel, ok := c.reserveDelegate(message.DelegateID)
	if !ok {
		_ = c.write(context.Background(), DelegateResponseMessage(message.DelegateID, ResultErr[DelegateResponse](fmt.Sprintf("code-mode host exceeded the limit of %d pending delegate calls", MaxPendingDelegateCalls))))
		return
	}
	defer c.releaseDelegate(message.DelegateID, cancel)
	if delegate == nil || message.Request == nil {
		_ = c.write(context.Background(), DelegateResponseMessage(message.DelegateID, ResultErr[DelegateResponse]("code-mode session delegate is unavailable")))
		return
	}
	var response DelegateResponse
	var err error
	switch message.Request.Type {
	case "tool/invoke":
		if message.Request.Invocation == nil {
			err = fmt.Errorf("delegate tool invocation is missing")
			break
		}
		call := message.Request.Invocation
		kind := tool.PayloadFunction
		if call.ProtocolToolKind == ProtocolToolKindFreeform {
			kind = tool.PayloadCustom
		}
		name := tool.ToolName{Name: call.ToolName.Name}
		if call.ToolName.Namespace != nil {
			name.Namespace = *call.ToolName.Namespace
		}
		var result json.RawMessage
		result, err = delegate.Invoke(ctx, tool.CodeModeRemoteNestedCall{CellID: call.CellID.String(), RuntimeToolCallID: call.RuntimeToolCallID, ToolName: name, Kind: kind, Input: call.Input})
		response = ToolResultResponse(result)
	case "notification/send":
		err = delegate.Notify(ctx, message.Request.CallID, message.Request.CellID.String(), message.Request.Text)
		response = NotificationDeliveredResponse()
	default:
		err = fmt.Errorf("unknown code-mode delegate request %q", message.Request.Type)
	}
	if err != nil {
		_ = c.write(context.Background(), DelegateResponseMessage(message.DelegateID, ResultErr[DelegateResponse](err.Error())))
		return
	}
	_ = c.write(context.Background(), DelegateResponseMessage(message.DelegateID, ResultOK(response)))
}

func (c *remoteConnection) reserveDelegate(id DelegateRequestID) (context.Context, context.CancelFunc, bool) {
	if c == nil {
		return nil, nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cancels) >= MaxPendingDelegateCalls {
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancels[id] = cancel
	return ctx, cancel, true
}

func (c *remoteConnection) releaseDelegate(id DelegateRequestID, cancel context.CancelFunc) {
	if cancel != nil {
		cancel()
	}
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.cancels, id)
	c.mu.Unlock()
}

func (c *remoteConnection) fail(err error) {
	c.mu.Lock()
	if !c.alive {
		c.mu.Unlock()
		return
	}
	c.alive = false
	pending := c.pending
	c.pending = map[RequestID]*remotePending{}
	cancels := c.cancels
	c.cancels = map[DelegateRequestID]context.CancelFunc{}
	c.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	for _, request := range pending {
		request.response <- remoteResult{err: err}
		if request.initial != nil {
			request.initial <- remoteInitialResult{err: err}
		}
	}
}

func (c *remoteConnection) Close() error {
	if c == nil {
		return nil
	}
	c.fail(fmt.Errorf("code-mode host connection closed"))
	return c.transport.Close()
}
