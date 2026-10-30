package appserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestTransientEnvironmentStartupFailureRetriesOnNextTurnLikeRust mirrors Rust
// #57866: a retryable failure while starting an environment must not be
// memoized on the thread selection; the next turn with the same workspace
// selection dials again and succeeds.
func TestTransientEnvironmentStartupFailureRetriesOnNextTurnLikeRust(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "transient startup failure", http.StatusInternalServerError)
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			t.Errorf("websocket accept error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := expectExecServerRequestForTest(ctx, conn, "initialize", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{"sessionId": "retry-session"})
		}); err != nil {
			t.Errorf("initialize error = %v", err)
			return
		}
		if err := expectExecServerRequestForTest(ctx, conn, "initialized", nil); err != nil {
			t.Errorf("initialized error = %v", err)
			return
		}
		if err := expectExecServerRequestForTest(ctx, conn, "environment/status", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{"status": "ready"})
		}); err != nil {
			t.Errorf("environment/status error = %v", err)
		}
	}))
	t.Cleanup(server.Close)

	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh"}, t.TempDir())
	if _, err := manager.Add(&EnvironmentAddParams{
		EnvironmentID: "remote",
		ExecServerURL: "ws" + strings.TrimPrefix(server.URL, "http"),
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	params := &EnvironmentStatusParams{EnvironmentID: "remote"}
	first, err := manager.Status(params)
	if err != nil {
		t.Fatalf("first status error = %v", err)
	}
	if first.Status != EnvironmentStatusDisconnected {
		t.Fatalf("first status = %#v, want disconnected after a transient startup failure", first)
	}
	if _, cached := manager.CachedInfo("remote"); cached {
		t.Fatal("failed startup must not be cached as environment info")
	}
	second, err := manager.Status(params)
	if err != nil {
		t.Fatalf("second status error = %v", err)
	}
	if second.Status != EnvironmentStatusReady {
		t.Fatalf("second status = %#v, want ready after retrying the startup", second)
	}
}
