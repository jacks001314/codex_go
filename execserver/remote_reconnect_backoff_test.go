package execserver

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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

// TestRemoteReconnectBackoffJitterWindowBoundsLikeRust mirrors Rust #50465
// `ef8cfe5e96`
// (remote/reconnect_backoff_tests.rs::reconnect_jitter_stays_within_each_backoff_window):
// every window jitters inside [backoff/2, backoff], both extremes are
// reachable, and distinct samples do not collapse onto one delay.
func TestRemoteReconnectBackoffJitterWindowBoundsLikeRust(t *testing.T) {
	for _, seconds := range []int64{1, 2, 4, 8, 16, 30} {
		window := time.Duration(seconds) * time.Second
		newWindow := func() *remoteReconnectBackoff {
			backoff := newRemoteReconnectBackoff(defaultRemoteBackoff, defaultRemoteMaxBackoff)
			backoff.delay = window
			return backoff
		}
		if got := newWindow().nextDelay(0); got != window/2 {
			t.Fatalf("nextDelay(0) over a %v window = %v, want the %v floor", window, got, window/2)
		}
		halfMillis := uint64(seconds) * 500
		if got := newWindow().nextDelay(halfMillis); got != window {
			t.Fatalf("nextDelay(%d) over a %v window = %v, want the full window", halfMillis, window, got)
		}
		for _, sample := range []uint64{1, 1234, 987654, math.MaxUint64} {
			delay := newWindow().nextDelay(sample)
			if delay < window/2 || delay > window {
				t.Fatalf("nextDelay(%d) over a %v window = %v, want within [%v, %v]", sample, window, delay, window/2, window)
			}
		}
		if first, second := newWindow().nextDelay(1), newWindow().nextDelay(2); first == second {
			t.Fatalf("nextDelay(1) == nextDelay(2) == %v over a %v window, want distinct jitter", first, window)
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
