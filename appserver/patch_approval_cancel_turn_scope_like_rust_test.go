package appserver

import (
	"context"
	"os"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

// fileChangeCancelRouter builds a router that reaches the user approval stage
// for a file change: no session-wide approval, no PermissionRequest hook, and
// the default user reviewer.
func fileChangeCancelRouter(t *testing.T) (*RuntimeRouter, string) {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"on-request\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{})
	t.Cleanup(func() { _ = router.Close() })
	return router, cwd
}

func patchApprovalRequest(callID string) *tool.ApplyPatchApprovalRequest {
	return &tool.ApplyPatchApprovalRequest{
		Patch:      "*** Begin Patch\n*** End Patch\n",
		Invocation: &tool.Invocation{CallID: callID},
	}
}

// TestFileChangeApprovalCancelInterruptsRequestingTurnLikeRust pins Rust #59910:
// a file change approval answered with ReviewDecision::Abort cancels the pending
// approval and aborts the turn that issued it, the same way the command approval
// menu's abort decision already does.
func TestFileChangeApprovalCancelInterruptsRequestingTurnLikeRust(t *testing.T) {
	router, cwd := fileChangeCancelRouter(t)
	const threadID = "thread-patch-cancel"
	started, err := router.requireTurns().Start(&turn.TurnStartParams{ThreadID: threadID})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	aborted := make(chan struct{})
	if err := router.threads.RegisterTurn(threadID, started.Turn.ID, func() { close(aborted) }, 0, &turn.TurnStartParams{ThreadID: threadID, CWD: cwd, Model: "gpt-test"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestFileChangeApproval {
			t.Errorf("server request method = %q, want %q", request.Method, ServerRequestFileChangeApproval)
		}
		_, _ = router.requireServerRequests().Resolve(OK(request.ID, &FileChangeRequestApprovalResponse{Decision: FileChangeApprovalCancel}))
	}))

	decision, err := router.applyPatchApprovalForTurn(threadID, started.Turn.ID)(context.Background(), patchApprovalRequest("call-patch-cancel"))
	if err != nil {
		t.Fatalf("apply patch approval error = %v", err)
	}
	if decision.Approved {
		t.Fatalf("cancel decision = %#v, want not approved", decision)
	}
	select {
	case <-aborted:
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling a file change approval did not abort the requesting turn")
	}
}

// TestPatchApprovalCancelStaysScopedToRequestingTurnLikeRust replays Rust #59910's
// regression: cancel one turn's approval, then deliver that same cancellation
// again while a later turn has a pending approval with the same call id. The
// stale cancellation must not resolve the new approval and must not abort the
// later turn.
func TestPatchApprovalCancelStaysScopedToRequestingTurnLikeRust(t *testing.T) {
	router, cwd := fileChangeCancelRouter(t)
	const threadID = "thread-patch-scope"

	first, err := router.requireTurns().Start(&turn.TurnStartParams{ThreadID: threadID})
	if err != nil {
		t.Fatalf("Start(turn-1) error = %v", err)
	}
	if err := router.threads.RegisterTurn(threadID, first.Turn.ID, nil, 0, &turn.TurnStartParams{ThreadID: threadID, CWD: cwd, Model: "gpt-test"}); err != nil {
		t.Fatalf("RegisterTurn(turn-1) error = %v", err)
	}

	var (
		firstRequestID RequestID
		requests       int
		replayResolved bool
		replayErr      error
	)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requests++
		if requests == 1 {
			firstRequestID = request.ID
			_, _ = router.requireServerRequests().Resolve(OK(request.ID, &FileChangeRequestApprovalResponse{Decision: FileChangeApprovalCancel}))
			return
		}
		// The later turn's approval is pending here with the same call id the
		// first turn used. Replaying the stale cancellation must find nothing.
		replayResolved, replayErr = router.requireServerRequests().Resolve(OK(firstRequestID, &FileChangeRequestApprovalResponse{Decision: FileChangeApprovalCancel}))
		_, _ = router.requireServerRequests().Resolve(OK(request.ID, &FileChangeRequestApprovalResponse{Decision: FileChangeApprovalAccept}))
	}))

	if _, err := router.applyPatchApprovalForTurn(threadID, first.Turn.ID)(context.Background(), patchApprovalRequest("call-patch-replay")); err != nil {
		t.Fatalf("apply patch approval (turn-1) error = %v", err)
	}

	second, err := router.requireTurns().Start(&turn.TurnStartParams{ThreadID: threadID})
	if err != nil {
		t.Fatalf("Start(turn-2) error = %v", err)
	}
	if err := router.threads.RegisterTurn(threadID, second.Turn.ID, nil, 0, &turn.TurnStartParams{ThreadID: threadID, CWD: cwd, Model: "gpt-test"}); err != nil {
		t.Fatalf("RegisterTurn(turn-2) error = %v", err)
	}

	decision, err := router.applyPatchApprovalForTurn(threadID, second.Turn.ID)(context.Background(), patchApprovalRequest("call-patch-replay"))
	if err != nil {
		t.Fatalf("apply patch approval (turn-2) error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("approval requests = %d, want 2", requests)
	}
	if replayErr != nil {
		t.Fatalf("stale cancellation replay error = %v", replayErr)
	}
	if replayResolved {
		t.Fatal("stale cancellation resolved the later turn's pending approval")
	}
	if !decision.Approved {
		t.Fatalf("later turn approval = %#v, want approved", decision)
	}
	if got := router.threads.ActiveTurnID(threadID); got != second.Turn.ID {
		t.Fatalf("active turn after stale cancellation = %q, want %q", got, second.Turn.ID)
	}
}
