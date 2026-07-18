package remotecontrol

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const remoteControlWebsocketLoopStatusPollInterval = 100 * time.Millisecond

type RemoteControlWebsocketConnectFunc func(ctx context.Context, options *RemoteControlWebsocketConnectOptions) (*websocket.Conn, *http.Response, error)

type RemoteControlReconnectDelayFunc func(reconnectAttempt *uint64) (time.Duration, bool)

type RemoteControlAuthRevisionFunc func(ctx context.Context) (uint64, error)

type RemoteControlWebsocketLoopOptions struct {
	BufferSize                 int
	Connect                    RemoteControlWebsocketConnectFunc
	ReconnectDelay             RemoteControlReconnectDelayFunc
	AuthRevision               RemoteControlAuthRevisionFunc
	StatusPollInterval         time.Duration
	ConnectionShutdownTimeout  time.Duration
	ClientTrackerOptions       *ClientTrackerOptions
	PingInterval               time.Duration
	PingTimeout                time.Duration
	WebsocketWriteTimeout      time.Duration
	WebsocketIdleSweepInterval time.Duration
}

type RemoteControlWebsocketLoop struct {
	manager         *Manager
	serverEvents    chan QueuedServerEnvelope
	transportEvents chan RemoteClientTransportEvent
	state           *WebsocketState
	stateMu         sync.Mutex
	clientTracker   *ClientTracker
	clientTrackerMu sync.Mutex
	options         RemoteControlWebsocketLoopOptions
}

type remoteControlConnectionEndReason string

const (
	remoteControlConnectionEndedShutdown remoteControlConnectionEndReason = "shutdown"
	remoteControlConnectionEndedDisabled remoteControlConnectionEndReason = "disabled"
	remoteControlConnectionEndedWorker   remoteControlConnectionEndReason = "connection_worker_stopped"
)

type remoteControlReconnectWaitReason string

const (
	remoteControlReconnectWaitReady       remoteControlReconnectWaitReason = "ready"
	remoteControlReconnectWaitShutdown    remoteControlReconnectWaitReason = "shutdown"
	remoteControlReconnectWaitAuthChanged remoteControlReconnectWaitReason = "auth_changed"
)

func NewRemoteControlWebsocketLoop(manager *Manager, options *RemoteControlWebsocketLoopOptions) *RemoteControlWebsocketLoop {
	if options == nil {
		options = &RemoteControlWebsocketLoopOptions{}
	}
	bufferSize := options.BufferSize
	if bufferSize <= 0 {
		bufferSize = DefaultRemoteControlChannelBufferSize
	}
	serverEvents := make(chan QueuedServerEnvelope, bufferSize)
	transportEvents := make(chan RemoteClientTransportEvent, bufferSize)
	trackerOptions := ClientTrackerOptions{}
	if options.ClientTrackerOptions != nil {
		trackerOptions = *options.ClientTrackerOptions
	}
	if trackerOptions.BufferSize <= 0 {
		trackerOptions.BufferSize = bufferSize
	}
	return &RemoteControlWebsocketLoop{
		manager:         manager,
		serverEvents:    serverEvents,
		transportEvents: transportEvents,
		state:           NewWebsocketState(),
		clientTracker:   NewClientTracker(serverEvents, transportEvents, &trackerOptions),
		options:         *options,
	}
}

func (l *RemoteControlWebsocketLoop) TransportEvents() <-chan RemoteClientTransportEvent {
	if l == nil {
		return nil
	}
	return l.transportEvents
}

func (l *RemoteControlWebsocketLoop) Run(ctx context.Context) error {
	if l == nil || l.manager == nil {
		return fmt.Errorf("%w: remote control websocket loop manager is nil", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), l.connectionShutdownTimeout())
		defer cancel()
		l.clientTrackerMu.Lock()
		l.clientTracker.Shutdown(shutdownCtx)
		l.clientTrackerMu.Unlock()
		close(l.transportEvents)
	}()

	var reconnectAttempt uint64
	var authRevision uint64
	var authRevisionKnown bool
	for {
		if !l.waitUntilEnabled(ctx) {
			return nil
		}
		l.observeAuthRevision(ctx, &authRevision, &authRevisionKnown)
		l.manager.PublishConnectionStatus(StatusConnecting)
		conn, err := l.connect(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !l.remoteControlEnabled() {
				continue
			}
			l.manager.PublishConnectionStatus(StatusErrored)
			delay := l.nextReconnectDelay(&reconnectAttempt)
			l.markRecoveryAuthChangeSeenIfNeeded(ctx, &authRevision, &authRevisionKnown)
			reason := l.waitForReconnect(ctx, delay, &authRevision, &authRevisionKnown)
			if reason == remoteControlReconnectWaitShutdown {
				return nil
			}
			if reason == remoteControlReconnectWaitAuthChanged {
				reconnectAttempt = 0
				l.manager.ResetAuthRecovery()
			}
			continue
		}

		reconnectAttempt = 0
		l.manager.ResetAuthRecovery()
		l.observeAuthRevision(ctx, &authRevision, &authRevisionKnown)
		l.manager.PublishConnectionStatus(StatusConnected)
		reason := l.runConnection(ctx, conn)
		if reason == remoteControlConnectionEndedShutdown {
			return nil
		}
		if reason == remoteControlConnectionEndedDisabled {
			continue
		}
		if !l.remoteControlEnabled() {
			continue
		}
		l.manager.PublishConnectionStatus(StatusErrored)
		delay := l.nextReconnectDelay(&reconnectAttempt)
		l.markRecoveryAuthChangeSeenIfNeeded(ctx, &authRevision, &authRevisionKnown)
		waitReason := l.waitForReconnect(ctx, delay, &authRevision, &authRevisionKnown)
		if waitReason == remoteControlReconnectWaitShutdown {
			return nil
		}
		if waitReason == remoteControlReconnectWaitAuthChanged {
			reconnectAttempt = 0
			l.manager.ResetAuthRecovery()
		}
	}
}

func (l *RemoteControlWebsocketLoop) connect(ctx context.Context) (*websocket.Conn, error) {
	var cursor *string
	l.stateMu.Lock()
	if l.state != nil && l.state.SubscribeCursor != nil {
		value := *l.state.SubscribeCursor
		cursor = &value
	}
	l.stateMu.Unlock()
	options := &RemoteControlWebsocketConnectOptions{SubscribeCursor: cursor}
	connect := l.options.Connect
	if connect == nil {
		connect = l.manager.ConnectWebsocketContext
	}
	conn, _, err := connect(ctx, options)
	return conn, err
}

func (l *RemoteControlWebsocketLoop) runConnection(ctx context.Context, conn *websocket.Conn) remoteControlConnectionEndReason {
	if conn == nil {
		return remoteControlConnectionEndedWorker
	}
	connectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.Close(websocket.StatusNormalClosure, "")

	workers := &RemoteControlWebsocketConnectionWorkers{
		Conn:            conn,
		State:           l.state,
		StateMu:         &l.stateMu,
		ClientTracker:   l.clientTracker,
		ClientTrackerMu: &l.clientTrackerMu,
		ServerEvents:    l.serverEvents,
		PingInterval:    l.options.PingInterval,
		PingTimeout:     l.options.PingTimeout,
		WriteTimeout:    l.options.WebsocketWriteTimeout,
		IdleSweep:       l.options.WebsocketIdleSweepInterval,
	}
	errCh := make(chan error, 2)
	go func() {
		errCh <- workers.RunServerWriter(connectionCtx)
	}()
	go func() {
		errCh <- workers.RunWebsocketReader(connectionCtx)
	}()

	statusTicker := time.NewTicker(l.statusPollInterval())
	defer statusTicker.Stop()
	reason := remoteControlConnectionEndedWorker
	select {
	case <-ctx.Done():
		reason = remoteControlConnectionEndedShutdown
	case <-statusTicker.C:
		if !l.remoteControlEnabled() {
			reason = remoteControlConnectionEndedDisabled
		} else {
			reason = l.waitConnectionEnd(ctx, errCh, statusTicker)
		}
	case <-errCh:
		reason = remoteControlConnectionEndedWorker
	}
	cancel()
	_ = conn.Close(websocket.StatusNormalClosure, "")
	l.waitConnectionWorkers(errCh)
	return reason
}

func (l *RemoteControlWebsocketLoop) waitConnectionEnd(ctx context.Context, errCh <-chan error, ticker *time.Ticker) remoteControlConnectionEndReason {
	for {
		select {
		case <-ctx.Done():
			return remoteControlConnectionEndedShutdown
		case <-ticker.C:
			if !l.remoteControlEnabled() {
				return remoteControlConnectionEndedDisabled
			}
		case <-errCh:
			return remoteControlConnectionEndedWorker
		}
	}
}

func (l *RemoteControlWebsocketLoop) waitConnectionWorkers(errCh <-chan error) {
	timeout := time.NewTimer(l.connectionShutdownTimeout())
	defer timeout.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-errCh:
		case <-timeout.C:
			return
		}
	}
}

func (l *RemoteControlWebsocketLoop) waitUntilEnabled(ctx context.Context) bool {
	if l.remoteControlEnabled() {
		return true
	}
	ticker := time.NewTicker(l.statusPollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if l.remoteControlEnabled() {
				return true
			}
		}
	}
}

func (l *RemoteControlWebsocketLoop) waitForReconnect(ctx context.Context, delay time.Duration, authRevision *uint64, authRevisionKnown *bool) remoteControlReconnectWaitReason {
	if delay <= 0 {
		return remoteControlReconnectWaitReady
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	ticker := time.NewTicker(l.statusPollInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return remoteControlReconnectWaitShutdown
		case <-timer.C:
			return remoteControlReconnectWaitReady
		case <-ticker.C:
			if !l.remoteControlEnabled() {
				return remoteControlReconnectWaitReady
			}
			if l.observeAuthRevision(ctx, authRevision, authRevisionKnown) {
				return remoteControlReconnectWaitAuthChanged
			}
		}
	}
}

func (l *RemoteControlWebsocketLoop) observeAuthRevision(ctx context.Context, authRevision *uint64, authRevisionKnown *bool) bool {
	current, ok := l.currentAuthRevision(ctx)
	if !ok || authRevision == nil || authRevisionKnown == nil {
		return false
	}
	if !*authRevisionKnown {
		*authRevision = current
		*authRevisionKnown = true
		return false
	}
	if current == *authRevision {
		return false
	}
	*authRevision = current
	return true
}

func (l *RemoteControlWebsocketLoop) markSingleRecoveryAuthChangeSeen(ctx context.Context, authRevision *uint64, authRevisionKnown *bool) {
	current, ok := l.currentAuthRevision(ctx)
	if !ok || authRevision == nil || authRevisionKnown == nil {
		return
	}
	if !*authRevisionKnown {
		*authRevision = current
		*authRevisionKnown = true
		return
	}
	if current == *authRevision+1 {
		*authRevision = current
	}
}

func (l *RemoteControlWebsocketLoop) markRecoveryAuthChangeSeenIfNeeded(ctx context.Context, authRevision *uint64, authRevisionKnown *bool) {
	if l == nil || l.manager == nil || !l.manager.ConsumeAuthRecoveryChanged() {
		return
	}
	l.markSingleRecoveryAuthChangeSeen(ctx, authRevision, authRevisionKnown)
}

func (l *RemoteControlWebsocketLoop) currentAuthRevision(ctx context.Context) (uint64, bool) {
	if l == nil || l.options.AuthRevision == nil {
		return 0, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	revision, err := l.options.AuthRevision(ctx)
	if err != nil {
		return 0, false
	}
	return revision, true
}

func (l *RemoteControlWebsocketLoop) remoteControlEnabled() bool {
	if l == nil || l.manager == nil {
		return false
	}
	return l.manager.StatusChanged().Status != StatusDisabled
}

func (l *RemoteControlWebsocketLoop) nextReconnectDelay(reconnectAttempt *uint64) time.Duration {
	if l != nil && l.options.ReconnectDelay != nil {
		delay, _ := l.options.ReconnectDelay(reconnectAttempt)
		return delay
	}
	delay, _ := NextReconnectDelay(reconnectAttempt)
	return delay
}

func (l *RemoteControlWebsocketLoop) statusPollInterval() time.Duration {
	if l != nil && l.options.StatusPollInterval > 0 {
		return l.options.StatusPollInterval
	}
	return remoteControlWebsocketLoopStatusPollInterval
}

func (l *RemoteControlWebsocketLoop) connectionShutdownTimeout() time.Duration {
	if l != nil && l.options.ConnectionShutdownTimeout > 0 {
		return l.options.ConnectionShutdownTimeout
	}
	return RemoteControlConnectionShutdownTimeout
}
