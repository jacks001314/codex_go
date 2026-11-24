package appserver

import (
	"context"
	"testing"

	"codex_go/session"
	"codex_go/turn"
)

// sessionStartCancelFixture builds a router whose hook registry holds one
// SessionStart hook with the requested execution mode.
func sessionStartCancelFixture(t *testing.T, mode HookExecutionMode) (*RuntimeRouter, string) {
	t.Helper()
	cwd := t.TempDir()
	store := session.NewStore(t.TempDir())
	if err := store.Save(&session.Record{ID: "thread-1"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	registry := NewHookRegistry()
	metadata := hookRunnerMetadata("start-"+string(mode), HookEventSessionStart, "", 0)
	metadata.ExecutionMode = mode
	metadata.SourcePath = cwd
	if err := registry.Add(cwd, metadata); err != nil {
		t.Fatalf("registry.Add() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		HookRunner:   NewHookRunner(),
		Hooks:        registry,
		DefaultCWD:   cwd,
	})
	return router, cwd
}

// TestCancelledSessionStartHookDefersSynchronousMatchLikeRust pins Rust #63523:
// when cancellation was already requested, a SessionStart event with a matching
// synchronous hook stays queued for the next turn, while an event that matches
// only asynchronous hooks still runs and clears.
func TestCancelledSessionStartHookDefersSynchronousMatchLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode       HookExecutionMode
		wantQueued bool
	}{
		{name: "synchronous hook is deferred", mode: HookExecutionSync, wantQueued: true},
		{name: "asynchronous hook still runs", mode: HookExecutionAsync, wantQueued: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, cwd := sessionStartCancelFixture(t, tc.mode)
			if err := router.markThreadPendingSessionStartSource("thread-1", SessionStartSourceStartup); err != nil {
				t.Fatalf("markThreadPendingSessionStartSource() error = %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := router.runPendingSessionStartHook(ctx, &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd})
			if err != nil {
				t.Fatalf("runPendingSessionStartHook() error = %v", err)
			}
			if _, _, queued, peekErr := router.peekPendingSessionStartSource("thread-1"); peekErr != nil {
				t.Fatalf("peek error = %v", peekErr)
			} else if queued != tc.wantQueued {
				t.Fatalf("event queued = %v, want %v", queued, tc.wantQueued)
			}
		})
	}

	// A live turn runs the synchronous hook and clears the event.
	router, cwd := sessionStartCancelFixture(t, HookExecutionSync)
	if err := router.markThreadPendingSessionStartSource("thread-1", SessionStartSourceStartup); err != nil {
		t.Fatalf("markThreadPendingSessionStartSource() error = %v", err)
	}
	if err := router.runPendingSessionStartHook(context.Background(), &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd}); err != nil {
		t.Fatalf("live turn error = %v", err)
	}
	if _, _, queued, peekErr := router.peekPendingSessionStartSource("thread-1"); peekErr != nil || queued {
		t.Fatalf("live turn left the event queued: queued=%v err=%v", queued, peekErr)
	}
}
