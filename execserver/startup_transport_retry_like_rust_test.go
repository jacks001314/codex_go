package execserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// compressStartupTransportRetry shrinks the transport retry window and its
// backoff so the tests do not wait on production timings.
func compressStartupTransportRetry(t *testing.T, window time.Duration) {
	t.Helper()
	previousWindow := startupTransportRetryTimeout
	previousInitial := registryRecoveryInitialRetryMillis
	previousMax := registryRecoveryMaxRetryInterval
	startupTransportRetryTimeout = window
	registryRecoveryInitialRetryMillis = 1
	registryRecoveryMaxRetryInterval = 2 * time.Millisecond
	t.Cleanup(func() {
		startupTransportRetryTimeout = previousWindow
		registryRecoveryInitialRetryMillis = previousInitial
		registryRecoveryMaxRetryInterval = previousMax
	})
}

func websocketURLForServer(server *httptest.Server) string {
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

// Rust #62601: a WebSocket that closes during initialize admits another
// physical attempt, so callers no longer receive the transient failure.
func TestDialClientRetriesStartupTransportAfterInitializeClosesLikeRust(t *testing.T) {
	compressStartupTransportRetry(t, 3*time.Second)
	var connections int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		attempt := atomic.AddInt32(&connections, 1)
		ctx := r.Context()
		if _, _, err := conn.Read(ctx); err != nil {
			return
		}
		if attempt == 1 {
			// The executor received initialize, but the caller never sees its
			// response.
			return
		}
		payload, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]any{"sessionId": "replacement-session"},
		})
		if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
			return
		}
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := DialClientWithOptions(ctx, websocketURLForServer(server), DialClientOptions{ClientName: "startup-retry-test"})
	if err != nil {
		t.Fatalf("DialClientWithOptions() error = %v", err)
	}
	defer client.Close()
	if got := client.SessionID(); got != "replacement-session" {
		t.Fatalf("session id = %q, want replacement-session", got)
	}
	if got := atomic.LoadInt32(&connections); got < 2 {
		t.Fatalf("connection attempts = %d, want at least 2", got)
	}
}

// Rust #62601: retry admission stops after the five-second window, while an
// attempt already underway may finish.
func TestDialClientStopsStartupTransportRetriesAtTheWindowLikeRust(t *testing.T) {
	compressStartupTransportRetry(t, 60*time.Millisecond)
	var connections int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		atomic.AddInt32(&connections, 1)
		_, _, _ = conn.Read(r.Context())
	}))
	defer server.Close()

	started := time.Now()
	_, err := DialClientWithOptions(context.Background(), websocketURLForServer(server), DialClientOptions{ClientName: "startup-window-test"})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("DialClientWithOptions() succeeded, want a transport failure")
	}
	if got := atomic.LoadInt32(&connections); got < 2 {
		t.Fatalf("connection attempts = %d, want at least 2", got)
	}
	if elapsed < 50*time.Millisecond {
		t.Fatalf("startup retry window closed after %v, want at least 50ms", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("startup retry window took %v", elapsed)
	}
}

// Registry and authentication failures keep their own terminal policy.
func TestDialClientDoesNotRetryPermanentStartupFailureLikeRust(t *testing.T) {
	compressStartupTransportRetry(t, 2*time.Second)
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		http.Error(w, fmt.Sprintf("unauthorized %d", atomic.LoadInt32(&requests)), http.StatusUnauthorized)
	}))
	defer server.Close()

	if _, err := DialClientWithOptions(context.Background(), websocketURLForServer(server), DialClientOptions{ClientName: "startup-terminal-test"}); err == nil {
		t.Fatal("DialClientWithOptions() succeeded, want an authentication failure")
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("handshake requests = %d, want 1", got)
	}
}
