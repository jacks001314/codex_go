package execserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type initializeTimeoutNoiseProvider struct {
	bundle *NoiseRendezvousConnectBundle
}

func (p *initializeTimeoutNoiseProvider) ConnectBundle(context.Context, RemotePublicKey) (*NoiseRendezvousConnectBundle, error) {
	bundle := *p.bundle
	return &bundle, nil
}

// Mirrors Rust #42623: a Noise handshake that never becomes ready is bounded by
// the configured initialization timeout, and the timed-out transport is closed.
func TestNoiseHandshakeUsesInitializeTimeoutLikeRust(t *testing.T) {
	executorIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate executor identity: %v", err)
	}
	defer executorIdentity.Destroy()

	handshakeFramesRead := make(chan struct{}, 1)
	connectionClosed := make(chan struct{})
	var closeOnce sync.Once
	signalClosed := func() {
		closeOnce.Do(func() { close(connectionClosed) })
	}
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			signalClosed()
			return
		}
		defer conn.CloseNow()
		defer signalClosed()
		for range 2 {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
		handshakeFramesRead <- struct{}{}
		// Stall the responder until the client's initialization deadline closes
		// the websocket.
		_, _, _ = conn.Read(r.Context())
	}))
	defer relay.Close()

	provider := &initializeTimeoutNoiseProvider{bundle: &NoiseRendezvousConnectBundle{
		WebSocketURL:            "ws" + strings.TrimPrefix(relay.URL, "http"),
		EnvironmentID:           "environment-1",
		ExecutorRegistrationID:  "registration-1",
		ExecutorPublicKey:       executorIdentity.PublicKey(),
		HarnessKeyAuthorization: "authorization-1",
	}}
	const initializeTimeout = 50 * time.Millisecond
	started := time.Now()
	_, err = DialNoiseRendezvousClient(context.Background(), provider, DialClientOptions{
		ClientName:        "initialize-timeout-test",
		InitializeTimeout: initializeTimeout,
	})
	var timeoutErr *InitializeTimedOutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("DialNoiseRendezvousClient() error = %v, want InitializeTimedOutError", err)
	}
	if timeoutErr.Timeout != initializeTimeout {
		t.Fatalf("timeout diagnostic = %v, want %v", timeoutErr.Timeout, initializeTimeout)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("stalled Noise handshake took %v, want it bounded by %v", elapsed, initializeTimeout)
	}
	select {
	case <-handshakeFramesRead:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not send the Noise handshake frames")
	}
	select {
	case <-connectionClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed-out Noise handshake did not close its websocket")
	}
}

// Mirrors Rust #42623: the handshake and initialize RPC share one budget, while
// a timeout after the handshake still reports the configured duration.
func TestNoiseInitializeRPCTimeoutReportsConfiguredBudgetLikeRust(t *testing.T) {
	executorIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate executor identity: %v", err)
	}
	defer executorIdentity.Destroy()

	rpcSeen := make(chan struct{}, 1)
	relayDone := make(chan error, 1)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			relayDone <- err
			return
		}
		defer conn.CloseNow()
		_, payload, err := conn.Read(r.Context())
		if err != nil {
			relayDone <- err
			return
		}
		resume, err := decodeRelayMessageFrame(payload)
		if err != nil {
			relayDone <- err
			return
		}
		streamID := resume.StreamID
		_, payload, err = conn.Read(r.Context())
		if err != nil {
			relayDone <- err
			return
		}
		handshakeFrame, err := decodeRelayMessageFrame(payload)
		if err != nil {
			relayDone <- err
			return
		}
		pending, _, err := readRemoteNoiseHandshake(
			executorIdentity,
			noiseChannelPrologue("environment-1", "registration-1", streamID),
			handshakeFrame.HandshakeBytes,
		)
		if err != nil {
			relayDone <- err
			return
		}
		transport, response, err := pending.Complete()
		if err != nil {
			relayDone <- err
			return
		}
		defer transport.Destroy()
		encoded, err := encodeRelayMessageFrame(newRelayHandshakeFrame(streamID, response))
		if err != nil {
			relayDone <- err
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageBinary, encoded); err != nil {
			relayDone <- err
			return
		}
		for {
			_, payload, err := conn.Read(r.Context())
			if err != nil {
				// The initialization timeout closes the websocket.
				relayDone <- nil
				return
			}
			frame, err := decodeRelayMessageFrame(payload)
			if err != nil || frame.StreamID != streamID || frame.Kind != relayFrameData {
				continue
			}
			rpcSeen <- struct{}{}
			break
		}
		// Read the initialize request but never answer it.
		_, _, _ = conn.Read(r.Context())
		relayDone <- nil
	}))
	defer relay.Close()

	provider := &initializeTimeoutNoiseProvider{bundle: &NoiseRendezvousConnectBundle{
		WebSocketURL:            "ws" + strings.TrimPrefix(relay.URL, "http"),
		EnvironmentID:           "environment-1",
		ExecutorRegistrationID:  "registration-1",
		ExecutorPublicKey:       executorIdentity.PublicKey(),
		HarnessKeyAuthorization: "authorization-1",
	}}
	const initializeTimeout = 200 * time.Millisecond
	_, err = DialNoiseRendezvousClient(context.Background(), provider, DialClientOptions{
		ClientName:        "initialize-rpc-timeout-test",
		InitializeTimeout: initializeTimeout,
	})
	var timeoutErr *InitializeTimedOutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("DialNoiseRendezvousClient() error = %v, want InitializeTimedOutError", err)
	}
	if timeoutErr.Timeout != initializeTimeout {
		t.Fatalf("timeout diagnostic = %v, want configured %v", timeoutErr.Timeout, initializeTimeout)
	}
	select {
	case <-rpcSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not send the initialize RPC after the handshake")
	}
	select {
	case err := <-relayDone:
		if err != nil {
			t.Fatalf("relay handler error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initialization timeout did not close the Noise websocket")
	}
}

// Direct WebSocket transports also bound the initialize RPC with the same
// configured timeout.
func TestDirectInitializeRPCUsesConfiguredInitializeTimeoutLikeRust(t *testing.T) {
	rpcSeen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		rpcSeen <- struct{}{}
		_, _, _ = conn.Read(r.Context())
	}))
	defer server.Close()

	const initializeTimeout = 50 * time.Millisecond
	_, err := DialClientWithOptions(
		context.Background(),
		"ws"+strings.TrimPrefix(server.URL, "http"),
		DialClientOptions{ClientName: "direct-initialize-timeout", InitializeTimeout: initializeTimeout},
	)
	var timeoutErr *InitializeTimedOutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("DialClientWithOptions() error = %v, want InitializeTimedOutError", err)
	}
	if timeoutErr.Timeout != initializeTimeout {
		t.Fatalf("timeout diagnostic = %v, want %v", timeoutErr.Timeout, initializeTimeout)
	}
	select {
	case <-rpcSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive the initialize RPC")
	}
}
