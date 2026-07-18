package remotecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	RemoteControlWebsocketPingInterval       = 10 * time.Second
	RemoteControlWebsocketPongTimeout        = 60 * time.Second
	RemoteControlConnectionShutdownTimeout   = 5 * time.Second
	remoteControlDefaultIdleSweepInterval    = RemoteControlIdleSweepInterval
	remoteControlDefaultWebsocketWriteTimout = 30 * time.Second
)

type RemoteControlWebsocketConnectionWorkers struct {
	Conn            *websocket.Conn
	State           *WebsocketState
	StateMu         *sync.Mutex
	ClientTracker   *ClientTracker
	ClientTrackerMu *sync.Mutex
	ServerEvents    <-chan QueuedServerEnvelope
	PingInterval    time.Duration
	PingTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleSweep       time.Duration
}

func (w *RemoteControlWebsocketConnectionWorkers) RunServerWriter(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := w.ensureWriter(); err != nil {
		return err
	}
	for _, envelope := range w.stateSnapshotEnvelopes() {
		if err := w.writeServerEnvelope(ctx, &envelope); err != nil {
			return err
		}
	}
	pingInterval := w.PingInterval
	if pingInterval <= 0 {
		pingInterval = RemoteControlWebsocketPingInterval
	}
	pingTimeout := w.PingTimeout
	if pingTimeout <= 0 {
		pingTimeout = RemoteControlWebsocketPongTimeout
	}
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
			err := w.Conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return fmt.Errorf("remote control websocket ping failed: %w", err)
			}
		case queued, ok := <-w.ServerEvents:
			if !ok {
				return io.ErrUnexpectedEOF
			}
			payloads, writeComplete, err := w.prepareQueuedServerEnvelope(&queued)
			if err != nil {
				return err
			}
			for _, payload := range payloads {
				if err := w.writeText(ctx, payload); err != nil {
					return err
				}
			}
			if writeComplete != nil {
				select {
				case writeComplete <- struct{}{}:
				default:
				}
			}
		}
	}
}

func (w *RemoteControlWebsocketConnectionWorkers) RunWebsocketReader(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := w.ensureReader(); err != nil {
		return err
	}
	idleSweep := w.IdleSweep
	if idleSweep <= 0 {
		idleSweep = remoteControlDefaultIdleSweepInterval
	}
	ticker := time.NewTicker(idleSweep)
	defer ticker.Stop()
	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()
	readCh := make(chan websocketReadResult, 1)
	startRead := func() {
		go func() {
			messageType, payload, err := w.Conn.Read(readCtx)
			readCh <- websocketReadResult{MessageType: messageType, Payload: payload, Err: err}
		}()
	}
	startRead()
	for {
		if err := w.drainClosedClients(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.closeExpiredClients(ctx); err != nil {
				return err
			}
		case result := <-readCh:
			if result.Err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("failed to read from websocket: %w", result.Err)
			}
			startRead()
			if result.MessageType == websocket.MessageBinary {
				continue
			}
			if result.MessageType != websocket.MessageText {
				continue
			}
			if err := w.handleClientPayload(ctx, result.Payload); err != nil {
				return err
			}
		}
	}
}

type websocketReadResult struct {
	MessageType websocket.MessageType
	Payload     []byte
	Err         error
}

func (w *RemoteControlWebsocketConnectionWorkers) ensureWriter() error {
	if w == nil || w.Conn == nil {
		return fmt.Errorf("%w: remote control websocket connection is nil", ErrInvalidRequest)
	}
	if w.State == nil {
		return fmt.Errorf("%w: remote control websocket state is nil", ErrInvalidRequest)
	}
	if w.ServerEvents == nil {
		return fmt.Errorf("%w: remote control server event channel is nil", ErrInvalidRequest)
	}
	return nil
}

func (w *RemoteControlWebsocketConnectionWorkers) ensureReader() error {
	if w == nil || w.Conn == nil {
		return fmt.Errorf("%w: remote control websocket connection is nil", ErrInvalidRequest)
	}
	if w.State == nil {
		return fmt.Errorf("%w: remote control websocket state is nil", ErrInvalidRequest)
	}
	if w.ClientTracker == nil {
		return fmt.Errorf("%w: remote control client tracker is nil", ErrInvalidRequest)
	}
	return nil
}

func (w *RemoteControlWebsocketConnectionWorkers) stateSnapshotEnvelopes() []ServerEnvelope {
	w.lockState()
	defer w.unlockState()
	w.State.ensure()
	return w.State.OutboundBuffer.Envelopes()
}

func (w *RemoteControlWebsocketConnectionWorkers) prepareQueuedServerEnvelope(queued *QueuedServerEnvelope) ([]string, chan<- struct{}, error) {
	if queued == nil {
		return nil, nil, nil
	}
	w.lockState()
	defer w.unlockState()
	w.State.ensure()
	envelope := cloneServerEnvelope(&queued.Envelope)
	envelope.SeqID = w.State.NextServerEnvelopeSeqID(envelope.ClientID, envelope.StreamID)
	envelopes, err := SplitServerEnvelopeForTransport(&envelope)
	if err != nil {
		return nil, nil, err
	}
	payloads := make([]string, 0, len(envelopes))
	for i := range envelopes {
		payload, err := json.Marshal(&envelopes[i])
		if err != nil {
			return nil, nil, err
		}
		w.State.QueueServerEnvelope(&envelopes[i])
		payloads = append(payloads, string(payload))
	}
	return payloads, queued.WriteComplete, nil
}

func (w *RemoteControlWebsocketConnectionWorkers) writeServerEnvelope(ctx context.Context, envelope *ServerEnvelope) error {
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return w.writeText(ctx, string(payload))
}

func (w *RemoteControlWebsocketConnectionWorkers) writeText(ctx context.Context, payload string) error {
	timeout := w.WriteTimeout
	if timeout <= 0 {
		timeout = remoteControlDefaultWebsocketWriteTimout
	}
	writeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := w.Conn.Write(writeCtx, websocket.MessageText, []byte(payload)); err != nil {
		return fmt.Errorf("failed to write remote control websocket message: %w", err)
	}
	return nil
}

func (w *RemoteControlWebsocketConnectionWorkers) handleClientPayload(ctx context.Context, payload []byte) error {
	var envelope ClientEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil
	}
	observation := w.observeClientEnvelope(&envelope, len(payload))
	if observation.Type != ClientSegmentForward || observation.Envelope == nil {
		return nil
	}
	delivered := cloneClientEnvelope(observation.Envelope)
	closedClientID := delivered.ClientID
	var closedStreamID *StreamID
	if delivered.StreamID != nil {
		value := *delivered.StreamID
		closedStreamID = &value
	}
	isClosed := delivered.Type == ClientEventClientClosed
	w.lockTracker()
	err := w.ClientTracker.HandleMessage(ctx, observation.Envelope)
	w.unlockTracker()
	if err != nil {
		return err
	}
	w.recordClientEnvelopeDelivery(delivered)
	if isClosed {
		w.invalidateClosedClient(closedClientID, closedStreamID)
	}
	return nil
}

func (w *RemoteControlWebsocketConnectionWorkers) observeClientEnvelope(envelope *ClientEnvelope, wireSize int) ClientSegmentObservation {
	w.lockState()
	defer w.unlockState()
	return w.State.ObserveClientMessage(envelope, wireSize)
}

func (w *RemoteControlWebsocketConnectionWorkers) recordClientEnvelopeDelivery(envelope *ClientEnvelope) {
	w.lockState()
	defer w.unlockState()
	w.State.RecordClientMessageDelivery(envelope)
}

func (w *RemoteControlWebsocketConnectionWorkers) invalidateClosedClient(clientID ClientID, streamID *StreamID) {
	w.lockState()
	defer w.unlockState()
	if streamID == nil {
		w.State.InvalidateClientMessageClient(clientID)
		return
	}
	w.State.InvalidateClientMessageStream(clientID, *streamID)
}

func (w *RemoteControlWebsocketConnectionWorkers) drainClosedClients(ctx context.Context) error {
	w.lockTracker()
	keys := w.ClientTracker.DrainClosedClients()
	for _, key := range keys {
		if err := w.ClientTracker.CloseClient(ctx, key.clientID, key.streamID); err != nil {
			w.unlockTracker()
			return err
		}
	}
	w.unlockTracker()
	if len(keys) == 0 {
		return nil
	}
	w.lockState()
	defer w.unlockState()
	for _, key := range keys {
		w.State.InvalidateClientMessageStream(key.clientID, key.streamID)
	}
	return nil
}

func (w *RemoteControlWebsocketConnectionWorkers) closeExpiredClients(ctx context.Context) error {
	w.lockTracker()
	keys, err := w.ClientTracker.CloseExpiredClients(ctx)
	w.unlockTracker()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	w.lockState()
	defer w.unlockState()
	for _, key := range keys {
		w.State.InvalidateClientMessageStream(key.clientID, key.streamID)
	}
	return nil
}

func (w *RemoteControlWebsocketConnectionWorkers) lockState() {
	if w != nil && w.StateMu != nil {
		w.StateMu.Lock()
	}
}

func (w *RemoteControlWebsocketConnectionWorkers) unlockState() {
	if w != nil && w.StateMu != nil {
		w.StateMu.Unlock()
	}
}

func (w *RemoteControlWebsocketConnectionWorkers) lockTracker() {
	if w != nil && w.ClientTrackerMu != nil {
		w.ClientTrackerMu.Lock()
	}
}

func (w *RemoteControlWebsocketConnectionWorkers) unlockTracker() {
	if w != nil && w.ClientTrackerMu != nil {
		w.ClientTrackerMu.Unlock()
	}
}
