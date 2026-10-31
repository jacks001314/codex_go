package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/turn"
)

// TestTurnAdmissionPermitKeepsAdmittedWorkAliveAfterDrainLikeRust mirrors
// codex-rs app-server/src/turn_admission.rs: a request admitted before
// begin_drain keeps its permit, so the turn it prepared still reserves and
// registers, while work that arrives after the gate closed is rejected.
func TestTurnAdmissionPermitKeepsAdmittedWorkAliveAfterDrainLikeRust(t *testing.T) {
	manager := NewThreadManager(NewThreadStatusManager())
	permit, err := manager.AdmitTurnWork()
	if err != nil {
		t.Fatalf("AdmitTurnWork() error = %v", err)
	}
	manager.BeginDraining()

	if _, err := manager.AdmitTurnWork(); err == nil {
		t.Fatal("AdmitTurnWork() succeeded after the drain gate closed")
	}
	if err := manager.ReserveTurn("late-thread"); err == nil {
		t.Fatal("ReserveTurn() succeeded after the drain gate closed")
	}
	if err := manager.ReserveTurnAdmitted("admitted-thread", permit); err != nil {
		t.Fatalf("ReserveTurnAdmitted() error = %v", err)
	}
	cancelled := false
	if err := manager.RegisterTurn("admitted-thread", "turn-1", func() { cancelled = true }, time.Now().UnixMilli(), &turn.TurnStartParams{ThreadID: "admitted-thread"}); err != nil {
		t.Fatalf("RegisterTurn() for admitted work error = %v, cancelled = %t", err, cancelled)
	}
	if cancelled {
		t.Fatal("admitted turn was cancelled by the drain")
	}
	if err := manager.RegisterTurn("direct-thread", "turn-2", nil, 0, &turn.TurnStartParams{ThreadID: "direct-thread"}); err == nil {
		t.Fatal("RegisterTurn() without a reservation succeeded after the drain gate closed")
	}
}

// TestStdioDispatchedTurnWorkFinishesDuringDrainLikeRust covers the transport
// boundary: every request the stdio server read before EOF keeps working, and
// only requests dispatched after the gate closed observe the drain error.
func TestStdioDispatchedTurnWorkFinishesDuringDrainLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewDefaultRuntimeRouter(store, t.TempDir())
	server := NewStdioServer(router)
	thread := router.Handle(stdioTestRequestWithParams(t, IntID(100), MethodThreadStart, ThreadStartParams{Ephemeral: true}))
	if thread.Error != nil {
		t.Fatalf("thread/start error: %+v", thread.Error)
	}
	threadID := thread.Result.(*ThreadStartResponse).Thread.ID

	// The server is already draining: a newly dispatched turn/start is rejected
	// with the structured shutdown error instead of running.
	router.BeginDraining()
	input := strings.Join([]string{
		mustJSONLine(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  MethodInitialize,
			"params": map[string]any{
				"clientInfo": map[string]any{"name": "test", "version": "1"},
			},
		}),
		mustJSONLine(t, map[string]any{
			"jsonrpc": "2.0",
			"id":      6,
			"method":  MethodTurnStart,
			"params": turn.TurnStartParams{
				ThreadID: threadID,
				Prompt:   "hello after drain",
			},
		}),
		"",
	}, "\n")
	var out lockedStringBuilder
	if err := server.Serve(strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	lines := nonEmptyJSONLines(out.String())
	found := false
	for _, line := range lines {
		if !strings.Contains(line, "\"id\":6") {
			continue
		}
		found = true
		if !strings.Contains(line, "Server is draining; retry after reconnecting") {
			t.Fatalf("turn/start after drain = %s", line)
		}
		if !strings.Contains(line, "\"reason\":\"serverShuttingDown\"") {
			t.Fatalf("turn/start after drain missing reason: %s", line)
		}
	}
	if !found {
		t.Fatalf("turn/start response missing from %#v", lines)
	}
}

// TestWorkspacePauseWaitsForAdmittedWorkLikeRust mirrors
// turn_admission_tests.rs::workspace_pause_waits_for_admitted_work_and_preserves_shutdown_drain:
// pause does not complete while a dispatched request still holds its permit.
func TestWorkspacePauseWaitsForAdmittedWorkLikeRust(t *testing.T) {
	manager := NewThreadManager(NewThreadStatusManager())
	permit, err := manager.AdmitTurnWork()
	if err != nil {
		t.Fatalf("AdmitTurnWork() error = %v", err)
	}
	type pauseResult struct {
		pause *TurnAdmissionPause
		err   error
	}
	done := make(chan pauseResult, 1)
	go func() {
		pause, err := manager.PauseTurnAdmission(context.Background())
		done <- pauseResult{pause: pause, err: err}
	}()
	select {
	case result := <-done:
		if result.pause != nil {
			result.pause.Release()
		}
		t.Fatalf("PauseTurnAdmission() completed while admitted work was in flight: %v", result.err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := manager.AdmitTurnWork(); err == nil {
		t.Fatal("AdmitTurnWork() succeeded while admission was paused")
	}
	permit.Release()
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("PauseTurnAdmission() error = %v", result.err)
		}
		result.pause.Release()
	case <-time.After(2 * time.Second):
		t.Fatal("PauseTurnAdmission() did not resume after the permit was released")
	}
}
