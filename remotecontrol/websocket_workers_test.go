package remotecontrol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWebsocketWorkersWriterAssignsSeqIDsPerStream(t *testing.T) {
	clientConn, serverConn := connectedRemoteControlWebsocketPair(t)
	serverEvents := make(chan QueuedServerEnvelope, 4)
	state := NewWebsocketState()
	workers := &RemoteControlWebsocketConnectionWorkers{
		Conn:         clientConn,
		State:        state,
		ServerEvents: serverEvents,
		PingInterval: time.Hour,
		WriteTimeout: time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- workers.RunServerWriter(ctx)
	}()
	clientID := ClientID("client-1")
	firstStream := StreamID("stream-1")
	secondStream := StreamID("stream-2")
	done := make(chan struct{}, 1)
	status := PongStatusActive
	for _, streamID := range []StreamID{firstStream, secondStream, firstStream} {
		serverEvents <- QueuedServerEnvelope{
			Envelope: ServerEnvelope{
				Type:     ServerEventPong,
				Status:   &status,
				ClientID: clientID,
				StreamID: streamID,
			},
			WriteComplete: done,
		}
	}

	first := readWebsocketServerEnvelope(t, serverConn)
	second := readWebsocketServerEnvelope(t, serverConn)
	third := readWebsocketServerEnvelope(t, serverConn)
	if first.SeqID != 1 || first.StreamID != firstStream || second.SeqID != 1 || second.StreamID != secondStream || third.SeqID != 2 || third.StreamID != firstStream {
		t.Fatalf("envelopes = %+v %+v %+v", first, second, third)
	}
	if got := state.OutboundBuffer.ForStream(clientID, firstStream); len(got) != 2 || got[0].SeqID != 1 || got[1].SeqID != 2 {
		t.Fatalf("first stream buffer = %+v", got)
	}
	cancel()
	_ = clientConn.CloseNow()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("writer error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("writer did not stop")
	}
}

func TestWebsocketWorkersReaderDeliversMessagesAndAcksOutbound(t *testing.T) {
	clientConn, serverConn := connectedRemoteControlWebsocketPair(t)
	serverEvents := make(chan QueuedServerEnvelope, 4)
	transportEvents := make(chan RemoteClientTransportEvent, 4)
	state := NewWebsocketState()
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{SendTimeout: time.Second})
	workers := &RemoteControlWebsocketConnectionWorkers{
		Conn:          clientConn,
		State:         state,
		ClientTracker: tracker,
		IdleSweep:     time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- workers.RunWebsocketReader(ctx)
	}()

	streamID := StreamID("stream-1")
	initialize := initializeClientEnvelope("client-1", string(streamID), uint64Ptr(0))
	writeClientEnvelope(t, serverConn, initialize)
	opened := readTransportEvent(t, transportEvents)
	if opened.Type != RemoteClientConnectionOpened || opened.ClientID != "client-1" || opened.StreamID != streamID {
		t.Fatalf("opened event = %+v", opened)
	}
	incoming := readTransportEvent(t, transportEvents)
	if incoming.Type != RemoteClientIncomingMessage || !json.Valid(incoming.Message) {
		t.Fatalf("incoming event = %+v", incoming)
	}

	state.QueueServerEnvelope(testPongEnvelope("client-1", streamID, 1))
	seqID := uint64(1)
	ack := &ClientEnvelope{
		Type:     ClientEventAck,
		ClientID: "client-1",
		StreamID: &streamID,
		SeqID:    &seqID,
	}
	writeClientEnvelope(t, serverConn, ack)
	deadline := time.After(2 * time.Second)
	for {
		if got := state.OutboundBuffer.ForStream("client-1", streamID); len(got) == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("ack did not clear outbound buffer: %+v", state.OutboundBuffer.ForStream("client-1", streamID))
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	cancel()
	_ = clientConn.CloseNow()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("reader error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("reader did not stop")
	}
}

func connectedRemoteControlWebsocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	serverConnCh := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("websocket accept: %v", err)
			return
		}
		serverConnCh <- conn
		<-r.Context().Done()
		_ = conn.CloseNow()
	}))
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		server.Close()
		t.Fatalf("websocket dial: %v", err)
	}
	var serverConn *websocket.Conn
	select {
	case serverConn = <-serverConnCh:
	case <-time.After(2 * time.Second):
		_ = clientConn.CloseNow()
		server.Close()
		t.Fatalf("server websocket was not accepted")
	}
	t.Cleanup(func() {
		_ = clientConn.CloseNow()
		_ = serverConn.CloseNow()
		server.Close()
	})
	return clientConn, serverConn
}

func readWebsocketServerEnvelope(t *testing.T, conn *websocket.Conn) ServerEnvelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	messageType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("websocket read: %v", err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("websocket message type = %v, want text", messageType)
	}
	var envelope ServerEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode server envelope %s: %v", payload, err)
	}
	return envelope
}

func writeClientEnvelope(t *testing.T, conn *websocket.Conn, envelope *ClientEnvelope) {
	t.Helper()
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal client envelope: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatalf("write client envelope: %v", err)
	}
}
