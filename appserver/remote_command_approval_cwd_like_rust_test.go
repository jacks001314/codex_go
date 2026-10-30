package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/tool"
	"codex_go/turn"
)

// Mirrors Rust #42577's approval half: the user command approval request reports
// the selected executor's working directory exactly as the executor resolved it
// (a remote path here), instead of a host-converted path.
func TestCommandApprovalCarriesTheTargetNativeCWDLikeRust(t *testing.T) {
	broker := NewServerRequestBroker()
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:     t.TempDir(),
		Config:         config.NewConfigService(t.TempDir()),
		ServerRequests: broker,
	})
	defer router.Close()
	requests := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requests <- request
	}))
	threadID := "thread-remote-approval"
	turnID := "turn-remote-approval"
	if err := router.threads.RegisterTurn(threadID, turnID, nil, 0, &turn.TurnStartParams{ThreadID: threadID, CWD: t.TempDir(), Model: "gpt-5.4"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	type approvalResult struct {
		decision tool.ShellApprovalDecision
		err      error
	}
	done := make(chan approvalResult, 1)
	go func() {
		decision, err := router.shellApprovalForTurn(threadID, turnID, false)(context.Background(), &tool.ShellApprovalRequest{
			Request: &tool.ShellRequest{
				HookCommand:              "touch marker",
				CWD:                      "/srv/project",
				UnifiedExecEnvironmentID: "remote",
				ApprovalReason:           "requires approval",
				SandboxPermissions:       sandbox.SandboxPermissionsUseDefault,
			},
			Invocation: &tool.Invocation{CallID: "call-remote-approval"},
		})
		done <- approvalResult{decision: decision, err: err}
	}()

	var pending *ServerRequest
	select {
	case pending = <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the command approval request")
	}
	params, ok := pending.Params.(*CommandExecutionRequestApprovalParams)
	if !ok || params.CWD == nil || *params.CWD != "/srv/project" {
		t.Fatalf("approval params = %#v, want the remote cwd", pending.Params)
	}
	if params.EnvironmentID == nil || *params.EnvironmentID != "remote" {
		t.Fatalf("approval environment = %#v, want remote", params.EnvironmentID)
	}
	if ok, err := broker.Resolve(OK(pending.ID, &CommandExecutionRequestApprovalResponse{Decision: CommandExecutionApprovalAccept})); err != nil || !ok {
		t.Fatalf("resolve approval = %v, %v", ok, err)
	}
	select {
	case result := <-done:
		if result.err != nil || !result.decision.Approved {
			t.Fatalf("approval result = %#v, %v", result.decision, result.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval callback did not return")
	}
}
