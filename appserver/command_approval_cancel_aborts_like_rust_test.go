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

// TestCancelCommandApprovalAbortsTurnLikeRust pins Rust's
// Session::resolve_exec_approval: the menu's "No, and tell Codex what to do
// differently" sends ReviewDecision::Abort, which interrupts the active turn
// instead of merely denying the command.
func TestCancelCommandApprovalAbortsTurnLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"on-request\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{})
	t.Cleanup(func() { _ = router.Close() })
	started, err := router.requireTurns().Start(&turn.TurnStartParams{ThreadID: "thread-cancel"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	aborted := make(chan struct{})
	if err := router.threads.RegisterTurn("thread-cancel", started.Turn.ID, func() { close(aborted) }, 0, &turn.TurnStartParams{ThreadID: "thread-cancel", CWD: cwd, Model: "gpt-test"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		response := OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: CommandExecutionApprovalCancel})
		router.requireServerRequests().Resolve(response)
	}))

	decision, err := router.shellApprovalForTurn("thread-cancel", started.Turn.ID, false)(context.Background(), &tool.ShellApprovalRequest{
		Request:    &tool.ShellRequest{HookCommand: "echo hi", CWD: cwd, ApprovalReason: "requires approval"},
		Invocation: &tool.Invocation{CallID: "call-cancel"},
	})
	if err != nil {
		t.Fatalf("shell approval error = %v", err)
	}
	if decision.Approved {
		t.Fatalf("cancel decision = %#v, want not approved", decision)
	}
	select {
	case <-aborted:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not abort the active turn")
	}
}
