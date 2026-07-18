package remotecontrol

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestClientTrackerInitializeOpensConnectionAndForwardsOutbound(t *testing.T) {
	serverEvents := make(chan QueuedServerEnvelope, 4)
	transportEvents := make(chan RemoteClientTransportEvent, 4)
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{SendTimeout: time.Second})

	if err := tracker.HandleMessage(context.Background(), initializeClientEnvelope("client-1", "stream-1", uint64Ptr(0))); err != nil {
		t.Fatalf("HandleMessage(initialize) error = %v", err)
	}
	opened := readTransportEvent(t, transportEvents)
	if opened.Type != RemoteClientConnectionOpened || opened.ConnectionID == 0 || opened.ClientID != "client-1" || opened.StreamID != "stream-1" || opened.Writer == nil || opened.Disconnect == nil {
		t.Fatalf("opened event = %+v", opened)
	}
	incoming := readTransportEvent(t, transportEvents)
	if incoming.Type != RemoteClientIncomingMessage || incoming.ConnectionID != opened.ConnectionID || !json.Valid(incoming.Message) {
		t.Fatalf("incoming event = %+v", incoming)
	}

	opened.Writer <- RemoteClientOutgoingMessage{Message: json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{}}`)}
	server := readServerEnvelope(t, serverEvents)
	if server.Envelope.Type != ServerEventServerMessage || server.Envelope.ClientID != "client-1" || server.Envelope.StreamID != "stream-1" || string(server.Envelope.Message) == "" {
		t.Fatalf("server envelope = %+v", server.Envelope)
	}
}

func TestClientTrackerDedupesDeliveredInboundSeqID(t *testing.T) {
	serverEvents := make(chan QueuedServerEnvelope, 4)
	transportEvents := make(chan RemoteClientTransportEvent, 4)
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{SendTimeout: time.Second})
	ctx := context.Background()
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("client-1", "stream-1", uint64Ptr(0))); err != nil {
		t.Fatalf("initialize error = %v", err)
	}
	_ = readTransportEvent(t, transportEvents)
	_ = readTransportEvent(t, transportEvents)

	message := clientMessageEnvelope("client-1", "stream-1", uint64Ptr(1), `{"jsonrpc":"2.0","method":"initialized"}`)
	if err := tracker.HandleMessage(ctx, message); err != nil {
		t.Fatalf("message error = %v", err)
	}
	incoming := readTransportEvent(t, transportEvents)
	if incoming.Type != RemoteClientIncomingMessage {
		t.Fatalf("incoming = %+v", incoming)
	}
	if err := tracker.HandleMessage(ctx, message); err != nil {
		t.Fatalf("duplicate message error = %v", err)
	}
	assertNoTransportEvent(t, transportEvents)
}

func TestClientTrackerLegacyInitializeWithoutStreamDoesNotRecordSeq(t *testing.T) {
	serverEvents := make(chan QueuedServerEnvelope, 4)
	transportEvents := make(chan RemoteClientTransportEvent, 4)
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{SendTimeout: time.Second})
	ctx := context.Background()
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("client-1", "", uint64Ptr(0))); err != nil {
		t.Fatalf("legacy initialize error = %v", err)
	}
	opened := readTransportEvent(t, transportEvents)
	if opened.StreamID == "" {
		t.Fatalf("legacy stream id was empty: %+v", opened)
	}
	_ = readTransportEvent(t, transportEvents)

	followup := &ClientEnvelope{
		Type:     ClientEventClientMessage,
		Message:  json.RawMessage(`{"jsonrpc":"2.0","method":"initialized"}`),
		ClientID: "client-1",
		SeqID:    uint64Ptr(0),
	}
	if err := tracker.HandleMessage(ctx, followup); err != nil {
		t.Fatalf("legacy followup error = %v", err)
	}
	incoming := readTransportEvent(t, transportEvents)
	if incoming.Type != RemoteClientIncomingMessage || incoming.ConnectionID != opened.ConnectionID {
		t.Fatalf("legacy incoming = %+v opened=%+v", incoming, opened)
	}
}

func TestClientTrackerInitializeSameStreamClosesOldConnection(t *testing.T) {
	serverEvents := make(chan QueuedServerEnvelope, 4)
	transportEvents := make(chan RemoteClientTransportEvent, 8)
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{SendTimeout: time.Second})
	ctx := context.Background()
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("client-1", "stream-1", uint64Ptr(0))); err != nil {
		t.Fatalf("first initialize error = %v", err)
	}
	firstOpened := readTransportEvent(t, transportEvents)
	_ = readTransportEvent(t, transportEvents)

	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("client-1", "stream-1", uint64Ptr(2))); err != nil {
		t.Fatalf("second initialize error = %v", err)
	}
	closed := readTransportEvent(t, transportEvents)
	if closed.Type != RemoteClientConnectionClosed || closed.ConnectionID != firstOpened.ConnectionID {
		t.Fatalf("closed = %+v first=%+v", closed, firstOpened)
	}
	secondOpened := readTransportEvent(t, transportEvents)
	if secondOpened.Type != RemoteClientConnectionOpened || secondOpened.ConnectionID == firstOpened.ConnectionID {
		t.Fatalf("second opened = %+v first=%+v", secondOpened, firstOpened)
	}
	_ = readTransportEvent(t, transportEvents)
}

func TestClientTrackerPingAndCloseExpiredClients(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	serverEvents := make(chan QueuedServerEnvelope, 4)
	transportEvents := make(chan RemoteClientTransportEvent, 4)
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{
		SendTimeout: time.Second,
		IdleTimeout: time.Minute,
		Now:         func() time.Time { return now },
	})
	ctx := context.Background()
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("client-1", "stream-1", uint64Ptr(0))); err != nil {
		t.Fatalf("initialize error = %v", err)
	}
	opened := readTransportEvent(t, transportEvents)
	_ = readTransportEvent(t, transportEvents)
	if err := tracker.HandleMessage(ctx, &ClientEnvelope{Type: ClientEventPing, ClientID: "client-1", StreamID: streamIDPtr("stream-1")}); err != nil {
		t.Fatalf("ping error = %v", err)
	}
	pong := readServerEnvelope(t, serverEvents)
	if pong.Envelope.Type != ServerEventPong || pong.Envelope.Status == nil || *pong.Envelope.Status != PongStatusActive {
		t.Fatalf("pong = %+v", pong.Envelope)
	}

	now = now.Add(2 * time.Minute)
	expired, err := tracker.CloseExpiredClients(ctx)
	if err != nil {
		t.Fatalf("CloseExpiredClients() error = %v", err)
	}
	if len(expired) != 1 || expired[0].clientID != "client-1" || expired[0].streamID != "stream-1" {
		t.Fatalf("expired = %+v", expired)
	}
	closed := readTransportEvent(t, transportEvents)
	if closed.Type != RemoteClientConnectionClosed || closed.ConnectionID != opened.ConnectionID {
		t.Fatalf("closed = %+v opened=%+v", closed, opened)
	}
}

func initializeClientEnvelope(clientID string, streamID string, seqID *uint64) *ClientEnvelope {
	return clientMessageEnvelope(clientID, streamID, seqID, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"remote-test","version":"0.1.0"}}}`)
}

func clientMessageEnvelope(clientID string, streamID string, seqID *uint64, message string) *ClientEnvelope {
	envelope := &ClientEnvelope{
		Type:     ClientEventClientMessage,
		Message:  json.RawMessage(message),
		ClientID: ClientID(clientID),
		SeqID:    seqID,
	}
	if streamID != "" {
		envelope.StreamID = streamIDPtr(streamID)
	}
	return envelope
}

func readTransportEvent(t *testing.T, events <-chan RemoteClientTransportEvent) RemoteClientTransportEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for transport event")
		return RemoteClientTransportEvent{}
	}
}

func readServerEnvelope(t *testing.T, events <-chan QueuedServerEnvelope) QueuedServerEnvelope {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for server envelope")
		return QueuedServerEnvelope{}
	}
}

func assertNoTransportEvent(t *testing.T, events <-chan RemoteClientTransportEvent) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected transport event: %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
}

func uint64Ptr(value uint64) *uint64 {
	return &value
}

func streamIDPtr(value string) *StreamID {
	streamID := StreamID(value)
	return &streamID
}
