package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Rust #57817: stopping an existing-call observer closes only its sideband
// transport. The frontend still owns the realtime session, so no session.close
// message may be sent.
func TestExistingCallDetachClosesOnlyTransportLikeRust(t *testing.T) {
	type frame struct {
		kind    websocket.MessageType
		payload string
	}
	var mu sync.Mutex
	var frames []frame
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.CloseNow()
		for {
			kind, payload, readErr := conn.Read(context.Background())
			if readErr != nil {
				close(closed)
				return
			}
			mu.Lock()
			frames = append(frames, frame{kind: kind, payload: string(payload)})
			mu.Unlock()
		}
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{SidebandBaseURL: server.URL})
	falseValue := false
	version := VersionV3
	if _, _, err := manager.Start(&StartParams{
		ThreadID:              "thread-existing-detach",
		OutputModality:        OutputAudio,
		Transport:             ExistingCallTransport("rtc_frontend_owned"),
		IncludeStartupContext: &falseValue,
		Version:               &version,
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	// Wait until the observer connected and the sideband is attached.
	deadline := time.Now().Add(5 * time.Second)
	for {
		manager.mu.Lock()
		connection := manager.connections["thread-existing-detach"]
		manager.mu.Unlock()
		if connection != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sideband observer never attached")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, _, err := manager.Stop(&StopParams{ThreadID: "thread-existing-detach"}, "requested"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the observer transport was never closed")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, received := range frames {
		if strings.Contains(received.payload, "session.close") {
			t.Fatalf("detaching the observer sent session.close: %#v", frames)
		}
	}
}
