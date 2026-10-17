package appserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type observedConnectionAttempt struct {
	elapsed time.Duration
	outcome ConnectionAttemptOutcome
}

func newConnectionAttemptObservation() (ConnectionObserver, <-chan observedConnectionAttempt) {
	observed := make(chan observedConnectionAttempt, 8)
	observer := func(elapsed time.Duration, outcome ConnectionAttemptOutcome) {
		observed <- observedConnectionAttempt{elapsed: elapsed, outcome: outcome}
	}
	return observer, observed
}

func waitForConnectionAttempt(t *testing.T, observed <-chan observedConnectionAttempt) observedConnectionAttempt {
	t.Helper()
	select {
	case attempt := <-observed:
		return attempt
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a connection attempt observation")
		return observedConnectionAttempt{}
	}
}

func expectNoConnectionAttempt(t *testing.T, observed <-chan observedConnectionAttempt) {
	t.Helper()
	select {
	case attempt := <-observed:
		t.Fatalf("unexpected connection attempt observation: %#v", attempt)
	case <-time.After(150 * time.Millisecond):
	}
}

// newRetryingEnvironmentStatusServer fails the first HTTP upgrade and then
// serves environment/status, so the first readiness check fails and a retry
// can succeed.
func newRetryingEnvironmentStatusServer(t *testing.T) string {
	t.Helper()
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "transient startup failure", http.StatusInternalServerError)
			return
		}
		// Delay the retry's handshake so the observed elapsed duration covers
		// the connection path rather than only bookkeeping.
		time.Sleep(25 * time.Millisecond)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			t.Errorf("websocket accept error = %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := expectExecServerRequestForTest(ctx, conn, "initialize", func(request map[string]any) error {
			return writeExecServerResponseForTest(ctx, conn, request["id"], map[string]any{"sessionId": "observer-session"})
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
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func TestEnvironmentObserveConnectionAttemptsReportsFailureThenSuccessLikeRust(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "env", ExecServerURL: newRetryingEnvironmentStatusServer(t)}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	observer, observed := newConnectionAttemptObservation()
	manager.ObserveConnectionAttempts("env", observer)

	if status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "env"}); err != nil {
		t.Fatalf("first Status() error = %v", err)
	} else if status.Status != EnvironmentStatusDisconnected {
		t.Fatalf("first Status() = %#v, want disconnected", status)
	}
	first := waitForConnectionAttempt(t, observed)
	if first.outcome != ConnectionAttemptOutcomeFailure {
		t.Fatalf("first outcome = %q, want %q", first.outcome, ConnectionAttemptOutcomeFailure)
	}

	if status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "env"}); err != nil {
		t.Fatalf("retry Status() error = %v", err)
	} else if status.Status != EnvironmentStatusReady {
		t.Fatalf("retry Status() = %#v, want ready", status)
	}
	retried := waitForConnectionAttempt(t, observed)
	if retried.outcome != ConnectionAttemptOutcomeSuccess {
		t.Fatalf("retry outcome = %q, want %q", retried.outcome, ConnectionAttemptOutcomeSuccess)
	}
	if retried.elapsed < 10*time.Millisecond {
		t.Fatalf("retry elapsed = %v, want at least the 25ms handshake delay", retried.elapsed)
	}

	// A reconnection after a successful initial connection is never reported.
	if _, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "env"}); err != nil {
		t.Fatalf("reconnect Status() error = %v", err)
	}
	expectNoConnectionAttempt(t, observed)
}

func TestEnvironmentObserveConnectionAttemptsFirstObserverWinsLikeRust(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "env", ExecServerURL: "ws://127.0.0.1:1"}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	first, firstObserved := newConnectionAttemptObservation()
	second, secondObserved := newConnectionAttemptObservation()
	manager.ObserveConnectionAttempts("env", first)
	manager.ObserveConnectionAttempts("env", second)

	if _, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "env"}); err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if attempt := waitForConnectionAttempt(t, firstObserved); attempt.outcome != ConnectionAttemptOutcomeFailure {
		t.Fatalf("first observer outcome = %q, want %q", attempt.outcome, ConnectionAttemptOutcomeFailure)
	}
	expectNoConnectionAttempt(t, secondObserved)
}

// newStalledEnvironmentServer accepts the upgrade and blocks until the client
// drops the connection, letting a test replace the environment mid-handshake.
func newStalledEnvironmentServer(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	accepted := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		once.Do(func() { close(accepted) })
		_, _, _ = conn.Read(r.Context())
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), accepted
}

func TestEnvironmentObserveConnectionAttemptsCancelledOnceWhenReplacedLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		reload func(t *testing.T, manager *EnvironmentManager, url string) error
	}{
		{
			name: "replace",
			reload: func(t *testing.T, manager *EnvironmentManager, url string) error {
				t.Helper()
				_, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "env", ExecServerURL: url})
				return err
			},
		},
		{
			name: "remove",
			reload: func(t *testing.T, manager *EnvironmentManager, url string) error {
				t.Helper()
				if !manager.Remove("env") {
					t.Fatal("Remove() = false, want true")
				}
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			url, accepted := newStalledEnvironmentServer(t)
			manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
			if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "env", ExecServerURL: url}); err != nil {
				t.Fatalf("Add() error = %v", err)
			}
			observer, observed := newConnectionAttemptObservation()
			manager.ObserveConnectionAttempts("env", observer)

			infoDone := make(chan error, 1)
			go func() {
				_, err := manager.Info(&EnvironmentInfoParams{EnvironmentID: "env"})
				infoDone <- err
			}()
			select {
			case <-accepted:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for the initial connection")
			}
			if err := test.reload(t, manager, url); err != nil {
				t.Fatalf("reload error = %v", err)
			}
			select {
			case err := <-infoDone:
				if err == nil {
					t.Fatal("Info() succeeded after startup was cancelled")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for Info() to unwind")
			}
			attempt := waitForConnectionAttempt(t, observed)
			if attempt.outcome != ConnectionAttemptOutcomeCancelled {
				t.Fatalf("outcome = %q, want %q", attempt.outcome, ConnectionAttemptOutcomeCancelled)
			}
			expectNoConnectionAttempt(t, observed)
		})
	}
}

func TestEnvironmentObserveConnectionAttemptsIgnoresPastAttemptsLikeRust(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	if _, err := manager.Add(&EnvironmentAddParams{EnvironmentID: "env", ExecServerURL: newRetryingEnvironmentStatusServer(t)}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	// The first attempt fails without an observer; the retry registers one.
	if _, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "env"}); err != nil {
		t.Fatalf("first Status() error = %v", err)
	}
	observer, observed := newConnectionAttemptObservation()
	manager.ObserveConnectionAttempts("env", observer)
	if status, err := manager.Status(&EnvironmentStatusParams{EnvironmentID: "env"}); err != nil {
		t.Fatalf("retry Status() error = %v", err)
	} else if status.Status != EnvironmentStatusReady {
		t.Fatalf("retry Status() = %#v, want ready", status)
	}
	attempt := waitForConnectionAttempt(t, observed)
	if attempt.outcome != ConnectionAttemptOutcomeSuccess {
		t.Fatalf("outcome = %q, want %q", attempt.outcome, ConnectionAttemptOutcomeSuccess)
	}
}
