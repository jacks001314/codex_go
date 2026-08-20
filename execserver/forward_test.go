package execserver

import (
	"context"
	"testing"
	"time"
)

func TestForwardServerConnectsToExistingExecServer(t *testing.T) {
	// Rust #39249: forwarding connects to an existing WebSocket exec-server
	// and serves complete payloads unchanged.
	serverCtx, cancelServer := context.WithCancel(context.Background())
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		cancelServer()
		t.Fatal("exec-server URL was not reported")
	}
	defer func() {
		cancelServer()
		select {
		case err := <-serverDone:
			if err != nil {
				t.Errorf("exec-server shutdown error = %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("exec-server did not stop")
		}
	}()

	forwardCtx, cancelForward := context.WithCancel(context.Background())
	forwardDone := make(chan error, 1)
	go func() {
		forwardDone <- ForwardServer(forwardCtx, &ForwardOptions{ConnectURL: serverURL})
	}()
	time.Sleep(200 * time.Millisecond)
	cancelForward()
	select {
	case err := <-forwardDone:
		if err != nil {
			t.Fatalf("ForwardServer error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ForwardServer did not stop after context cancellation")
	}
}

func TestForwardServerRejectsInvalidURL(t *testing.T) {
	if err := ForwardServer(context.Background(), &ForwardOptions{ConnectURL: "tcp://bad"}); err == nil {
		t.Fatal("invalid connect URL should be rejected")
	}
	if err := ForwardServer(context.Background(), &ForwardOptions{}); err == nil {
		t.Fatal("empty connect URL should be rejected")
	}
}
