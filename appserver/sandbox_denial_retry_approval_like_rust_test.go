package appserver

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

// TestSandboxDenialRetryApprovalCarriesRustRetryReason pins the operator-facing
// half of Rust's orchestrator retry: the escalated re-attempt reaches the client
// as an ordinary command-approval request whose reason is
// `build_denial_reason_from_output`'s sentence, with no additional permissions
// carried over from the sandboxed attempt.
func TestSandboxDenialRetryApprovalCarriesRustRetryReason(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-test\"\napproval_policy = \"on-request\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{})
	t.Cleanup(func() { _ = router.Close() })
	params := &turn.TurnStartParams{ThreadID: "thread-retry", CWD: cwd, Model: "gpt-test"}
	if err := router.threads.RegisterTurn("thread-retry", "turn-retry", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	requests := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requests <- request
	}))

	decisionCh := make(chan tool.ShellApprovalDecision, 1)
	errCh := make(chan error, 1)
	go func() {
		decision, err := router.shellApprovalForTurn("thread-retry", "turn-retry", false)(
			context.Background(),
			&tool.ShellApprovalRequest{
				Request: &tool.ShellRequest{
					HookCommand:      "git fetch origin main",
					CWD:              cwd,
					ApprovalReason:   sandboxDenialRetryReasonForTest,
					Justification:    sandboxDenialRetryReasonForTest,
					ApprovalRequired: true,
				},
				Invocation: &tool.Invocation{CallID: "call-retry"},
			},
		)
		decisionCh <- decision
		errCh <- err
	}()

	var request *ServerRequest
	select {
	case request = <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("retry approval request was not delivered")
	}
	if request.Method != ServerRequestCommandExecutionApproval {
		t.Fatalf("method = %q", request.Method)
	}
	approval, ok := request.Params.(*CommandExecutionRequestApprovalParams)
	if !ok {
		t.Fatalf("params type = %T", request.Params)
	}
	if approval.Reason == nil || strings.TrimSpace(*approval.Reason) != sandboxDenialRetryReasonForTest {
		t.Fatalf("reason = %#v", approval.Reason)
	}
	if approval.AdditionalPermissions != nil {
		t.Fatalf("retry carried the sandboxed attempt's permissions: %#v", approval.AdditionalPermissions)
	}
	if _, err := router.requireServerRequests().Resolve(OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: CommandExecutionApprovalAccept})); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	select {
	case decision := <-decisionCh:
		if err := <-errCh; err != nil {
			t.Fatalf("approval error = %v", err)
		}
		if !decision.Approved {
			t.Fatalf("decision = %#v, want approved", decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval did not resolve")
	}
}

// The reason text is owned by the tool package; this copy keeps the appserver
// test independent of its unexported constant while pinning the same wording.
const sandboxDenialRetryReasonForTest = "command failed; retry without sandbox?"
