package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Mirrors Rust #51502
// remote/reconnect_backoff_tests.rs::flapping_connections_retain_backoff_until_a_stable_session:
// short-lived connections keep the exponential delay, and only a connection
// that stayed stable for STABLE_CONNECTION_DURATION resets it.
func TestRemoteReconnectBackoffRetainsDelayUntilStableLikeRust(t *testing.T) {
	backoff := newRemoteReconnectBackoff(defaultRemoteBackoff, defaultRemoteMaxBackoff)
	for _, wantMillis := range []int64{500, 1000, 2000, 4000, 8000, 15000, 15000} {
		backoff.connectionClosed(remoteStableConnectionDuration - time.Millisecond)
		if got := backoff.nextDelay(0); got != time.Duration(wantMillis)*time.Millisecond {
			t.Fatalf("nextDelay(0) = %v after a short-lived connection, want %dms", got, wantMillis)
		}
	}
	backoff.connectionClosed(remoteStableConnectionDuration)
	if got := backoff.nextDelay(0); got != 500*time.Millisecond {
		t.Fatalf("nextDelay(0) = %v after a stable connection, want 500ms", got)
	}
	if got := backoff.nextDelay(0); got != time.Second {
		t.Fatalf("nextDelay(0) = %v after a stable connection, want 1s", got)
	}
}

// Mirrors Rust reconnect_jitter_stays_within_each_backoff_window: the jittered
// delay stays inside the [delay/2, delay] window and never exceeds the cap.
func TestRemoteReconnectBackoffJitterStaysWithinWindowLikeRust(t *testing.T) {
	backoff := newRemoteReconnectBackoff(defaultRemoteBackoff, defaultRemoteMaxBackoff)
	window := defaultRemoteBackoff
	for _, sample := range []uint64{0, 1, 499, 500, 501, 1_000_000} {
		delay := backoff.nextDelay(sample)
		if delay < window/2 || delay > window {
			t.Fatalf("nextDelay(%d) = %v, want within [%v, %v]", sample, delay, window/2, window)
		}
		if next := window * 2; next <= defaultRemoteMaxBackoff {
			window = next
		} else {
			window = defaultRemoteMaxBackoff
		}
	}
}

// Mirrors Rust #51502's bounded rendezvous attempts: a stalled WebSocket
// upgrade must not prevent reconnects, so every attempt carries the shared
// connect timeout and the loop keeps retrying.
func TestRendezvousDialAttemptsAreBoundedLikeRust(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(remoteRegistrationResponse{
			EnvironmentID:          "env-stalled",
			URL:                    "wss://rendezvous.invalid/relay?role=environment",
			SecurityProfile:        RemoteSecurityProfile,
			ExecutorRegistrationID: "registration-stalled",
		})
	}))
	defer registry.Close()

	var mu sync.Mutex
	attempts := 0
	dial := func(ctx context.Context, _ string, _ *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		deadline, ok := ctx.Deadline()
		remaining := time.Duration(0)
		if ok {
			remaining = time.Until(deadline)
		}
		mu.Lock()
		attempts++
		current := attempts
		mu.Unlock()
		if !ok {
			return nil, nil, errors.New("rendezvous dial context has no deadline")
		}
		if remaining <= 0 || remaining > defaultRemoteDialTimeout {
			return nil, nil, errors.New("rendezvous dial deadline is outside the shared connect timeout")
		}
		if current >= 2 {
			return nil, nil, context.Canceled
		}
		return nil, nil, errors.New("rendezvous upgrade stalled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunRemoteEnvironment(ctx, RemoteEnvironmentConfig{
			BaseURL:       registry.URL,
			EnvironmentID: "env-stalled",
			HTTPClient:    registry.Client(),
			Dial:          dial,
			Backoff:       time.Millisecond,
			MaxBackoff:    time.Millisecond,
		})
	}()

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		seen := attempts
		mu.Unlock()
		if seen >= 2 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("RunRemoteEnvironment exited early: %v", err)
		case <-deadline:
			t.Fatal("stalled rendezvous attempts did not retry")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunRemoteEnvironment returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunRemoteEnvironment did not stop after cancel")
	}
}
