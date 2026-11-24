package remotecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

const (
	RemoteControlClientIdleTimeout        = 10 * time.Minute
	RemoteControlIdleSweepInterval        = 30 * time.Second
	RemoteControlTransportEventTimeout    = 5 * time.Second
	DefaultRemoteControlChannelBufferSize = 64
)

type RemoteClientConnectionID uint64

type RemoteClientTransportEventType string

const (
	RemoteClientConnectionOpened RemoteClientTransportEventType = "connection_opened"
	RemoteClientIncomingMessage  RemoteClientTransportEventType = "incoming_message"
	RemoteClientConnectionClosed RemoteClientTransportEventType = "connection_closed"
)

type RemoteClientTransportEvent struct {
	Type         RemoteClientTransportEventType
	ConnectionID RemoteClientConnectionID
	ClientID     ClientID
	StreamID     StreamID
	Writer       chan RemoteClientOutgoingMessage
	Disconnect   func()
	Message      json.RawMessage
}

type RemoteClientOutgoingMessage struct {
	Message       json.RawMessage
	WriteComplete chan<- struct{}
	// ItemPage marks a thread/items/list response so the writer can report its
	// frame and write timing (Rust #59497).
	ItemPage bool
}

type QueuedServerEnvelope struct {
	Envelope      ServerEnvelope
	WriteComplete chan<- struct{}
	// ItemPage marks a thread/items/list response (Rust #59497).
	ItemPage bool
}

type ClientTrackerOptions struct {
	IdleTimeout time.Duration
	SendTimeout time.Duration
	Now         func() time.Time
	BufferSize  int
}

type ClientTracker struct {
	clients          map[envelopeStreamKey]*trackedRemoteClient
	legacyStreamIDs  map[ClientID]StreamID
	serverEnvelopeTx chan<- QueuedServerEnvelope
	transportEventTx chan<- RemoteClientTransportEvent
	closedClients    chan closedRemoteClient
	idleTimeout      time.Duration
	sendTimeout      time.Duration
	now              func() time.Time
	bufferSize       int
	// retriedWithController records whether the reconnect loop already
	// retried while a live controller was attached (Rust #63471).
	retriedWithController bool
}

type trackedRemoteClient struct {
	connectionID     RemoteClientConnectionID
	disconnect       chan struct{}
	writer           chan RemoteClientOutgoingMessage
	lastActivityAt   time.Time
	lastInboundSeqID *uint64
}

// closedRemoteClient reports an outbound writer that stopped, keeping the
// connection id so cleanup can skip a stream reused by a replacement
// connection (Rust #63471).
type closedRemoteClient struct {
	key          envelopeStreamKey
	connectionID RemoteClientConnectionID
}

var remoteClientConnectionCounter uint64

func NewClientTracker(serverEnvelopeTx chan<- QueuedServerEnvelope, transportEventTx chan<- RemoteClientTransportEvent, options *ClientTrackerOptions) *ClientTracker {
	if options == nil {
		options = &ClientTrackerOptions{}
	}
	idleTimeout := options.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = RemoteControlClientIdleTimeout
	}
	sendTimeout := options.SendTimeout
	if sendTimeout <= 0 {
		sendTimeout = RemoteControlTransportEventTimeout
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	bufferSize := options.BufferSize
	if bufferSize <= 0 {
		bufferSize = DefaultRemoteControlChannelBufferSize
	}
	return &ClientTracker{
		clients:          map[envelopeStreamKey]*trackedRemoteClient{},
		legacyStreamIDs:  map[ClientID]StreamID{},
		serverEnvelopeTx: serverEnvelopeTx,
		transportEventTx: transportEventTx,
		closedClients:    make(chan closedRemoteClient, bufferSize),
		idleTimeout:      idleTimeout,
		sendTimeout:      sendTimeout,
		now:              now,
		bufferSize:       bufferSize,
	}
}

// hasController reports whether a live client other than the relay is
// tracked, so a silent connection can reconnect on the shorter controller
// timeout and give the first retries the initial backoff (Rust #63471
// ClientTracker::has_controller).
func (t *ClientTracker) hasController() bool {
	if t == nil {
		return false
	}
	return t.hasControllerAt(t.now())
}

func (t *ClientTracker) hasControllerAt(now time.Time) bool {
	for key, client := range t.clients {
		if isLiveController(key.clientID, client, now, t.idleTimeout) {
			return true
		}
	}
	return false
}

// isLiveController mirrors Rust is_live_controller: the relay initializes its
// own client for every online host, so only other live clients count.
func isLiveController(id ClientID, client *trackedRemoteClient, now time.Time, idleTimeout time.Duration) bool {
	if client == nil || isRelayRemoteControlClient(id) {
		return false
	}
	select {
	case <-client.disconnect:
		return false
	default:
	}
	return now.Sub(client.lastActivityAt) < idleTimeout
}

// isRelayRemoteControlClient excludes the relay's own client, which every
// online host initializes (Rust is_live_controller).
func isRelayRemoteControlClient(id ClientID) bool {
	return strings.HasPrefix(string(id), "backend_")
}

// beginClientActivity samples liveness before a client starts or refreshes,
// clearing the retried flag once the last controller is gone so a returning
// controller gets the initial backoff again (Rust #63471
// ClientTracker::begin_client_activity).
func (t *ClientTracker) beginClientActivity() time.Time {
	now := t.now()
	if t.retriedWithController && !t.hasControllerAt(now) {
		t.retriedWithController = false
	}
	return now
}

// refreshClient samples activity first so a controller that expired while
// awaiting space is not revived (Rust ClientTracker::refresh_client).
func (t *ClientTracker) refreshClient(key envelopeStreamKey) *trackedRemoteClient {
	now := t.beginClientActivity()
	client := t.clients[key]
	if client == nil {
		return nil
	}
	client.lastActivityAt = now
	return client
}

func (t *ClientTracker) HandleMessage(ctx context.Context, envelope *ClientEnvelope) error {
	if t == nil || envelope == nil {
		return nil
	}
	t.ensure()
	clientID := envelope.ClientID
	isLegacyStreamID := envelope.StreamID == nil
	isInitialize := clientMessageStartsConnection(envelope.Message)
	streamID := t.resolveStreamID(clientID, envelope.StreamID, isInitialize, envelope.Type)
	if streamID == "" {
		return nil
	}
	key := envelopeStreamKey{clientID: clientID, streamID: streamID}
	switch envelope.Type {
	case ClientEventClientMessage:
		return t.handleClientMessage(ctx, key, envelope, isInitialize, isLegacyStreamID)
	case ClientEventClientMessageChunk, ClientEventAck:
		return nil
	case ClientEventPing:
		return t.handlePing(ctx, key)
	case ClientEventClientClosed:
		return t.CloseClient(ctx, clientID, streamID)
	default:
		return nil
	}
}

func (t *ClientTracker) NextClosedClient(ctx context.Context) (ClientID, StreamID, bool) {
	if t == nil {
		return "", "", false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case closed := <-t.closedClients:
			if t.staleClosedClient(closed) {
				continue
			}
			return closed.key.clientID, closed.key.streamID, true
		case <-ctx.Done():
			return "", "", false
		}
	}
}

func (t *ClientTracker) DrainClosedClients() []envelopeStreamKey {
	if t == nil {
		return nil
	}
	var keys []envelopeStreamKey
	for {
		select {
		case closed := <-t.closedClients:
			if t.staleClosedClient(closed) {
				continue
			}
			keys = append(keys, closed.key)
		default:
			return keys
		}
	}
}

// staleClosedClient ignores cleanup from an old writer when a replacement
// connection has reused the stream (Rust #63471 bookkeep_join_set).
func (t *ClientTracker) staleClosedClient(closed closedRemoteClient) bool {
	client := t.clients[closed.key]
	return client != nil && client.connectionID != closed.connectionID
}

func (t *ClientTracker) Shutdown(ctx context.Context) {
	if t == nil {
		return
	}
	t.ensure()
	keys := make([]envelopeStreamKey, 0, len(t.clients))
	for key := range t.clients {
		keys = append(keys, key)
	}
	for _, key := range keys {
		_ = t.CloseClient(ctx, key.clientID, key.streamID)
	}
}

func (t *ClientTracker) CloseExpiredClients(ctx context.Context) ([]envelopeStreamKey, error) {
	if t == nil {
		return nil, nil
	}
	t.ensure()
	now := t.now()
	var expired []envelopeStreamKey
	for key, client := range t.clients {
		if now.Sub(client.lastActivityAt) >= t.idleTimeout {
			expired = append(expired, key)
		}
	}
	for _, key := range expired {
		if err := t.CloseClient(ctx, key.clientID, key.streamID); err != nil {
			return expired, err
		}
	}
	return expired, nil
}

func (t *ClientTracker) CloseClient(ctx context.Context, clientID ClientID, streamID StreamID) error {
	if t == nil {
		return nil
	}
	t.ensure()
	key := envelopeStreamKey{clientID: clientID, streamID: streamID}
	client := t.removeClient(key)
	if client == nil {
		return nil
	}
	closeOnce(client.disconnect)
	return t.sendTransportEvent(ctx, RemoteClientTransportEvent{
		Type:         RemoteClientConnectionClosed,
		ConnectionID: client.connectionID,
		ClientID:     clientID,
		StreamID:     streamID,
	})
}

func (t *ClientTracker) handleClientMessage(ctx context.Context, key envelopeStreamKey, envelope *ClientEnvelope, isInitialize bool, isLegacyStreamID bool) error {
	now := t.now()
	existing := t.clients[key]
	// A sequenced initialize replay on the same stream keeps the live
	// controller's stream and terminals instead of restarting them
	// (Rust #63471).
	replacingLiveController := isInitialize && isLiveController(key.clientID, existing, now, t.idleTimeout)
	if envelope.SeqID != nil && existing != nil && existing.lastInboundSeqID != nil && *existing.lastInboundSeqID >= *envelope.SeqID {
		if !isInitialize {
			return nil
		}
		if !isLegacyStreamID && replacingLiveController {
			existing.lastActivityAt = now
			return nil
		}
	}
	if !isInitialize {
		client := t.refreshClient(key)
		if client == nil {
			return nil
		}
		if err := t.sendTransportEvent(ctx, RemoteClientTransportEvent{
			Type:         RemoteClientIncomingMessage,
			ConnectionID: client.connectionID,
			ClientID:     key.clientID,
			StreamID:     key.streamID,
			Message:      cloneRawMessage(envelope.Message),
		}); err != nil {
			return err
		}
		t.recordInboundMessageDelivery(key, envelope.SeqID)
		return nil
	}
	if t.clients[key] != nil {
		if err := t.CloseClient(ctx, key.clientID, key.streamID); err != nil {
			return err
		}
	}
	connectionID := RemoteClientConnectionID(atomic.AddUint64(&remoteClientConnectionCounter, 1))
	writer := make(chan RemoteClientOutgoingMessage, t.bufferSize)
	disconnect := make(chan struct{})
	if err := t.sendTransportEvent(ctx, RemoteClientTransportEvent{
		Type:         RemoteClientConnectionOpened,
		ConnectionID: connectionID,
		ClientID:     key.clientID,
		StreamID:     key.streamID,
		Writer:       writer,
		Disconnect: func() {
			closeOnce(disconnect)
		},
	}); err != nil {
		return err
	}
	// A legacy reinitialize replaces the connection on the same live stream;
	// that temporary removal is not a gap in controller activity, so keep the
	// activity sample instead of clearing the retried flag (Rust #63471).
	lastActivityAt := t.now()
	if !replacingLiveController {
		lastActivityAt = t.beginClientActivity()
	}
	t.clients[key] = &trackedRemoteClient{
		connectionID:   connectionID,
		disconnect:     disconnect,
		writer:         writer,
		lastActivityAt: lastActivityAt,
	}
	if isLegacyStreamID {
		t.legacyStreamIDs[key.clientID] = key.streamID
	}
	go t.runClientOutbound(connectionID, key, writer, disconnect)
	if err := t.sendTransportEvent(ctx, RemoteClientTransportEvent{
		Type:         RemoteClientIncomingMessage,
		ConnectionID: connectionID,
		ClientID:     key.clientID,
		StreamID:     key.streamID,
		Message:      cloneRawMessage(envelope.Message),
	}); err != nil {
		if client := t.removeClient(key); client != nil {
			closeOnce(client.disconnect)
			go func() {
				_ = t.sendTransportEvent(context.Background(), RemoteClientTransportEvent{
					Type:         RemoteClientConnectionClosed,
					ConnectionID: client.connectionID,
					ClientID:     key.clientID,
					StreamID:     key.streamID,
				})
			}()
		}
		return err
	}
	if !isLegacyStreamID {
		t.recordInboundMessageDelivery(key, envelope.SeqID)
	}
	return nil
}

func (t *ClientTracker) handlePing(ctx context.Context, key envelopeStreamKey) error {
	status := PongStatusUnknown
	if t.refreshClient(key) != nil {
		status = PongStatusActive
	}
	return t.sendServerEnvelope(ctx, QueuedServerEnvelope{
		Envelope: ServerEnvelope{
			Type:     ServerEventPong,
			Status:   &status,
			ClientID: key.clientID,
			StreamID: key.streamID,
		},
	})
}

func (t *ClientTracker) runClientOutbound(connectionID RemoteClientConnectionID, key envelopeStreamKey, writer <-chan RemoteClientOutgoingMessage, disconnect <-chan struct{}) {
	defer func() {
		select {
		case t.closedClients <- closedRemoteClient{key: key, connectionID: connectionID}:
		default:
		}
	}()
	for {
		select {
		case <-disconnect:
			return
		case outgoing, ok := <-writer:
			if !ok {
				return
			}
			err := t.sendServerEnvelope(context.Background(), QueuedServerEnvelope{
				ItemPage: outgoing.ItemPage,
				Envelope: ServerEnvelope{
					Type:     ServerEventServerMessage,
					Message:  cloneRawMessage(outgoing.Message),
					ClientID: key.clientID,
					StreamID: key.streamID,
				},
				WriteComplete: outgoing.WriteComplete,
			})
			if err != nil {
				return
			}
		}
	}
}

func (t *ClientTracker) resolveStreamID(clientID ClientID, streamID *StreamID, isInitialize bool, eventType ClientEventType) StreamID {
	if streamID != nil {
		return *streamID
	}
	if isInitialize {
		if existing := t.legacyStreamIDs[clientID]; existing != "" {
			delete(t.legacyStreamIDs, clientID)
			return existing
		}
		return NewStreamID()
	}
	if existing := t.legacyStreamIDs[clientID]; existing != "" {
		return existing
	}
	if eventType == ClientEventPing {
		return NewStreamID()
	}
	return ""
}

func (t *ClientTracker) removeClient(key envelopeStreamKey) *trackedRemoteClient {
	client := t.clients[key]
	if client == nil {
		return nil
	}
	delete(t.clients, key)
	if t.legacyStreamIDs[key.clientID] == key.streamID {
		delete(t.legacyStreamIDs, key.clientID)
	}
	return client
}

func (t *ClientTracker) recordInboundMessageDelivery(key envelopeStreamKey, seqID *uint64) {
	if seqID == nil {
		return
	}
	client := t.clients[key]
	if client == nil {
		return
	}
	value := *seqID
	client.lastInboundSeqID = &value
}

func (t *ClientTracker) sendTransportEvent(ctx context.Context, event RemoteClientTransportEvent) error {
	if t.transportEventTx == nil {
		return fmt.Errorf("%w: remote control transport event receiver is nil", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sendCtx, cancel := context.WithTimeout(ctx, t.sendTimeout)
	defer cancel()
	select {
	case t.transportEventTx <- event:
		return nil
	case <-sendCtx.Done():
		return sendCtx.Err()
	}
}

func (t *ClientTracker) sendServerEnvelope(ctx context.Context, envelope QueuedServerEnvelope) error {
	if t.serverEnvelopeTx == nil {
		return fmt.Errorf("%w: remote control server envelope receiver is nil", ErrInvalidRequest)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sendCtx, cancel := context.WithTimeout(ctx, t.sendTimeout)
	defer cancel()
	select {
	case t.serverEnvelopeTx <- envelope:
		return nil
	case <-sendCtx.Done():
		return sendCtx.Err()
	}
}

func (t *ClientTracker) ensure() {
	if t.clients == nil {
		t.clients = map[envelopeStreamKey]*trackedRemoteClient{}
	}
	if t.legacyStreamIDs == nil {
		t.legacyStreamIDs = map[ClientID]StreamID{}
	}
	if t.closedClients == nil {
		t.closedClients = make(chan closedRemoteClient, DefaultRemoteControlChannelBufferSize)
	}
	if t.idleTimeout <= 0 {
		t.idleTimeout = RemoteControlClientIdleTimeout
	}
	if t.sendTimeout <= 0 {
		t.sendTimeout = RemoteControlTransportEventTimeout
	}
	if t.now == nil {
		t.now = time.Now
	}
	if t.bufferSize <= 0 {
		t.bufferSize = DefaultRemoteControlChannelBufferSize
	}
}

func clientMessageStartsConnection(message json.RawMessage) bool {
	if len(message) == 0 {
		return false
	}
	var request struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(message, &request); err != nil {
		return false
	}
	return request.Method == "initialize"
}

func cloneRawMessage(message json.RawMessage) json.RawMessage {
	if len(message) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), message...)
}

func closeOnce(ch chan struct{}) {
	defer func() {
		_ = recover()
	}()
	close(ch)
}
