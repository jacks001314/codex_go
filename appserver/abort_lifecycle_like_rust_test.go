package appserver

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

// TestInterruptCallbackCompletesBeforeTurnAbortedLikeRust pins Rust #49475:
// the turn-abort lifecycle callback finishes before the terminal event, for an
// explicit interrupt, a review interrupt, a conditional extension interrupt,
// and shutdown.
func TestInterruptCallbackCompletesBeforeTurnAbortedLikeRust(t *testing.T) {
	operations := []struct {
		name       string
		originator string
		run        func(*RuntimeRouter, string, string) error
	}{
		{
			name: "interrupt",
			run: func(router *RuntimeRouter, threadID string, turnID string) error {
				_, err := router.interruptRuntimeTurn(threadID, turnID, nil, turnAbortReasonInterrupted)
				return err
			},
		},
		{
			name:       "review interrupt",
			originator: "review",
			run: func(router *RuntimeRouter, threadID string, turnID string) error {
				_, err := router.interruptRuntimeTurn(threadID, turnID, nil, turnAbortReasonInterrupted)
				return err
			},
		},
		{
			name: "conditional interrupt",
			run: func(router *RuntimeRouter, threadID string, turnID string) error {
				router.interruptTurnForGuardianCircuitBreaker(threadID, turnID, "denied repeatedly")
				return nil
			},
		},
		{
			name: "shutdown",
			run: func(router *RuntimeRouter, _ string, _ string) error {
				return router.Close()
			},
		},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			router, sink, threadID, turnID, cwd := newAbortLifecycleTestRouter(t)
			registerAbortLifecycleTurn(t, router, threadID, turnID, cwd, operation.originator)
			startedPath, releasePath := installBlockingInterruptHook(t, router, cwd)

			done := make(chan error, 1)
			go func() {
				done <- operation.run(router, threadID, turnID)
			}()

			waitForAbortLifecycleStart(t, startedPath, done)
			time.Sleep(100 * time.Millisecond)
			if completed := abortLifecycleCompletedCount(sink, turnID); completed != 0 {
				t.Fatalf("turn/completed count before the interrupt callback finished = %d, want 0", completed)
			}
			if err := os.WriteFile(releasePath, []byte("release"), 0o600); err != nil {
				t.Fatalf("release interrupt hook: %v", err)
			}

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("abort operation error = %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("abort operation did not finish after the interrupt callback was released")
			}
			waitForTurnCompletedStatus(t, sink, turnID, TurnStatusInterrupted)
			if completed := abortLifecycleCompletedCount(sink, turnID); completed != 1 {
				t.Fatalf("turn/completed count = %d, want 1", completed)
			}
			started, err := os.ReadFile(startedPath)
			if err != nil {
				t.Fatalf("read interrupt callback marker: %v", err)
			}
			if string(started) != "x" {
				t.Fatalf("interrupt callback invocations = %q, want one", started)
			}
		})
	}
}

func newAbortLifecycleTestRouter(t *testing.T) (*RuntimeRouter, *NotificationBuffer, string, string, string) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	threadID := "thread-abort-lifecycle"
	cwd := t.TempDir()
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:         cwd,
			HistoryMode: "legacy",
		},
	}); err != nil {
		t.Fatalf("Create(%s) error = %v", threadID, err)
	}
	turns := turn.NewTurnService()
	started, err := turns.Start(&turn.TurnStartParams{ThreadID: threadID, Prompt: "abort lifecycle"})
	if err != nil {
		t.Fatalf("TurnService.Start() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Turns: turns})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	t.Cleanup(func() { _ = router.Close() })
	return router, sink, threadID, started.Turn.ID, cwd
}

func registerAbortLifecycleTurn(t *testing.T, router *RuntimeRouter, threadID string, turnID string, cwd string, originator string) {
	t.Helper()
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	router.requireThreadStatus().NoteTurnStarted(threadID)
	if err := router.threads.ReserveTurn(threadID); err != nil {
		t.Fatalf("ReserveTurn() error = %v", err)
	}
	if err := router.threads.RegisterTurn(threadID, turnID, cancel, time.Now().UTC().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID, CWD: cwd, Originator: originator}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
}

func installBlockingInterruptHook(t *testing.T, router *RuntimeRouter, cwd string) (string, string) {
	t.Helper()
	startedPath := cwd + string(os.PathSeparator) + "interrupt-started"
	releasePath := cwd + string(os.PathSeparator) + "interrupt-release"
	hook := hookRunnerMetadata("abort-lifecycle", HookEventInterrupt, "", 0)
	hook.TimeoutSec = 3
	command := blockingInterruptHookCommand(startedPath, releasePath)
	hook.Command = &command
	if err := router.requireHooks().Add(cwd, hook); err != nil {
		t.Fatalf("add interrupt hook: %v", err)
	}
	return startedPath, releasePath
}

func blockingInterruptHookCommand(startedPath string, releasePath string) string {
	if runtime.GOOS == "windows" {
		script := "[IO.File]::AppendAllText(" + powerShellSingleQuote(startedPath) + ", 'x'); " +
			"while (-not (Test-Path -LiteralPath " + powerShellSingleQuote(releasePath) + ")) { Start-Sleep -Milliseconds 10 }"
		return powershellEncodedCommand(script)
	}
	return "printf x >> " + shellQuote(startedPath) + "; while [ ! -f " + shellQuote(releasePath) + " ]; do sleep 0.01; done"
}

func waitForAbortLifecycleStart(t *testing.T, path string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("abort operation finished before the interrupt callback started: %v", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for interrupt callback marker %s", path)
}

func abortLifecycleCompletedCount(sink *NotificationBuffer, turnID string) int {
	count := 0
	for _, notification := range sink.List() {
		if notification.Method != NotificationTurnCompleted {
			continue
		}
		completed, ok := notification.Params.(*TurnCompletedNotification)
		if ok && completed != nil && completed.Turn.ID == turnID {
			count++
		}
	}
	return count
}
