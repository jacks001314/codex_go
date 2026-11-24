package remotecontrol

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClientTrackerHasControllerLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	serverEvents := make(chan QueuedServerEnvelope, 8)
	transportEvents := make(chan RemoteClientTransportEvent, 8)
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{
		SendTimeout: time.Second,
		IdleTimeout: time.Minute,
		Now:         func() time.Time { return now },
	})
	ctx := context.Background()

	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("backend_host", "relay", uint64Ptr(0))); err != nil {
		t.Fatalf("relay initialize error = %v", err)
	}
	_ = readTransportEvent(t, transportEvents)
	_ = readTransportEvent(t, transportEvents)
	if tracker.hasController() {
		t.Fatal("relay client alone must not count as a controller")
	}

	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("controller", "stream-1", uint64Ptr(0))); err != nil {
		t.Fatalf("controller initialize error = %v", err)
	}
	_ = readTransportEvent(t, transportEvents)
	_ = readTransportEvent(t, transportEvents)
	if !tracker.hasController() {
		t.Fatal("live controller was not detected")
	}

	now = now.Add(2 * time.Minute)
	if tracker.hasController() {
		t.Fatal("expired controller must not count as live")
	}

	now = now.Add(time.Minute)
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("controller", "stream-1", uint64Ptr(5))); err != nil {
		t.Fatalf("controller reinitialize error = %v", err)
	}
	_ = readTransportEvent(t, transportEvents)
	_ = readTransportEvent(t, transportEvents)
	if !tracker.hasController() {
		t.Fatal("fresh client activity was not detected as a controller")
	}
	if err := tracker.CloseClient(ctx, "controller", "stream-1"); err != nil {
		t.Fatalf("CloseClient error = %v", err)
	}
	_ = readTransportEvent(t, transportEvents)
	if tracker.hasController() {
		t.Fatal("disconnected controller must not count as live")
	}
}

func TestNextReconnectDelayLikeRust(t *testing.T) {
	serverEvents := make(chan QueuedServerEnvelope, 8)
	transportEvents := make(chan RemoteClientTransportEvent, 8)
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{SendTimeout: time.Second})
	ctx := context.Background()
	var paced []string
	loop := &RemoteControlWebsocketLoop{
		clientTracker: tracker,
		options: RemoteControlWebsocketLoopOptions{
			ReconnectDelay: func(attempt *uint64) time.Duration {
				paced = append(paced, strconv.FormatUint(*attempt, 10))
				return time.Duration(*attempt+1) * time.Second
			},
		},
	}
	var attempt uint64
	for i := 0; i < 3; i++ {
		loop.nextReconnectDelay(&attempt)
	}
	if got := strings.Join(paced, ","); got != "0,1,2" {
		t.Fatalf("relay-only pacing = %v, want 0,1,2", got)
	}

	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("controller", "stream-1", uint64Ptr(0))); err != nil {
		t.Fatalf("controller initialize error = %v", err)
	}
	paced = nil
	for i := 0; i < 5; i++ {
		loop.nextReconnectDelay(&attempt)
	}
	if got := strings.Join(paced, ","); got != "0,0,0,0,0" {
		t.Fatalf("attached controller pacing = %v, want five initial attempts", got)
	}
	paced = nil
	loop.nextReconnectDelay(&attempt)
	if got := strings.Join(paced, ","); got != "1" {
		t.Fatalf("pacing after five initial attempts = %v, want 1", got)
	}

	if err := tracker.CloseClient(ctx, "controller", "stream-1"); err != nil {
		t.Fatalf("CloseClient error = %v", err)
	}
	paced = nil
	loop.nextReconnectDelay(&attempt)
	if len(paced) != 1 || paced[0] == "0" {
		t.Fatalf("relay-only pacing after controller left = %v, want a growing attempt", paced)
	}

	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("controller", "stream-2", uint64Ptr(0))); err != nil {
		t.Fatalf("controller reinitialize error = %v", err)
	}
	paced = nil
	loop.nextReconnectDelay(&attempt)
	if got := strings.Join(paced, ","); got != "0" {
		t.Fatalf("pacing after controller resumed = %v, want a reset attempt", got)
	}
}

func TestClientTrackerInitializeReplayKeepsLiveControllerStreamLikeRust(t *testing.T) {
	serverEvents := make(chan QueuedServerEnvelope, 8)
	transportEvents := make(chan RemoteClientTransportEvent, 8)
	now := time.Unix(1_700_000_000, 0)
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{
		SendTimeout: time.Second,
		Now:         func() time.Time { return now },
	})
	ctx := context.Background()
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("controller", "stream-1", uint64Ptr(0))); err != nil {
		t.Fatalf("initialize error = %v", err)
	}
	opened := readTransportEvent(t, transportEvents)
	_ = readTransportEvent(t, transportEvents)

	now = now.Add(30 * time.Second)
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("controller", "stream-1", uint64Ptr(0))); err != nil {
		t.Fatalf("replayed initialize error = %v", err)
	}
	assertNoTransportEvent(t, transportEvents)
	client := tracker.clients[envelopeStreamKey{clientID: "controller", streamID: "stream-1"}]
	if client == nil || client.connectionID != opened.ConnectionID {
		t.Fatalf("replayed initialize replaced the live stream: %+v", client)
	}
	if !client.lastActivityAt.Equal(now) {
		t.Fatalf("replayed initialize did not refresh activity: %v", client.lastActivityAt)
	}
}

func TestClientTrackerIgnoresCleanupFromReplacedWriterLikeRust(t *testing.T) {
	serverEvents := make(chan QueuedServerEnvelope, 8)
	transportEvents := make(chan RemoteClientTransportEvent, 8)
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{SendTimeout: time.Second})
	ctx := context.Background()
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("controller", "stream-1", uint64Ptr(0))); err != nil {
		t.Fatalf("initialize error = %v", err)
	}
	first := readTransportEvent(t, transportEvents)
	_ = readTransportEvent(t, transportEvents)
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("controller", "stream-1", uint64Ptr(5))); err != nil {
		t.Fatalf("reinitialize error = %v", err)
	}
	_ = readTransportEvent(t, transportEvents)
	second := readTransportEvent(t, transportEvents)
	if second.Type != RemoteClientConnectionOpened || second.ConnectionID == first.ConnectionID {
		t.Fatalf("replacement connection = %+v first=%+v", second, first)
	}
	_ = readTransportEvent(t, transportEvents)

	key := envelopeStreamKey{clientID: "controller", streamID: "stream-1"}
	tracker.closedClients <- closedRemoteClient{key: key, connectionID: first.ConnectionID}
	if keys := tracker.DrainClosedClients(); len(keys) != 0 {
		t.Fatalf("stale writer cleanup was not ignored: %+v", keys)
	}
	if client := tracker.clients[key]; client == nil || client.connectionID != second.ConnectionID {
		t.Fatalf("replacement connection was cleaned up: %+v", client)
	}
	assertNoTransportEvent(t, transportEvents)
}

func TestRunWebsocketReaderKeepsPongTimeoutForRelayOnlyLikeRust(t *testing.T) {
	clientConn, _ := connectedRemoteControlWebsocketPair(t)
	serverEvents := make(chan QueuedServerEnvelope, 8)
	transportEvents := make(chan RemoteClientTransportEvent, 8)
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{SendTimeout: time.Second})
	workers := &RemoteControlWebsocketConnectionWorkers{
		Conn:          clientConn,
		State:         NewWebsocketState(),
		ClientTracker: tracker,
		IdleSweep:     time.Hour,
		PingInterval:  20 * time.Millisecond,
		PingTimeout:   2 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("backend_host", "relay", uint64Ptr(0))); err != nil {
		t.Fatalf("relay initialize error = %v", err)
	}
	_ = readTransportEvent(t, transportEvents)
	_ = readTransportEvent(t, transportEvents)

	errCh := make(chan error, 1)
	go func() { errCh <- workers.RunWebsocketReader(ctx) }()
	select {
	case err := <-errCh:
		t.Fatalf("relay-only reader stopped early: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	_ = clientConn.CloseNow()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("reader error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not stop")
	}
}

func TestRunWebsocketReaderIdleTimeoutWithControllerLikeRust(t *testing.T) {
	clientConn, _ := connectedRemoteControlWebsocketPair(t)
	serverEvents := make(chan QueuedServerEnvelope, 8)
	transportEvents := make(chan RemoteClientTransportEvent, 8)
	tracker := NewClientTracker(serverEvents, transportEvents, &ClientTrackerOptions{SendTimeout: time.Second})
	workers := &RemoteControlWebsocketConnectionWorkers{
		Conn:          clientConn,
		State:         NewWebsocketState(),
		ClientTracker: tracker,
		IdleSweep:     time.Hour,
		PingInterval:  20 * time.Millisecond,
		PingTimeout:   2 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("backend_host", "relay", uint64Ptr(0))); err != nil {
		t.Fatalf("relay initialize error = %v", err)
	}
	_ = readTransportEvent(t, transportEvents)
	_ = readTransportEvent(t, transportEvents)
	if err := tracker.HandleMessage(ctx, initializeClientEnvelope("controller", "stream-1", uint64Ptr(0))); err != nil {
		t.Fatalf("controller initialize error = %v", err)
	}
	_ = readTransportEvent(t, transportEvents)
	_ = readTransportEvent(t, transportEvents)

	errCh := make(chan error, 1)
	go func() { errCh <- workers.RunWebsocketReader(ctx) }()
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "remote control websocket idle timeout") {
			t.Fatalf("reader error = %v, want idle timeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not detect the silent websocket")
	}
}
