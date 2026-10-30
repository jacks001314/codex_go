package appserver

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/execpolicy"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"
)

func managedCheckpointRequirements() *config.ConfigRequirements {
	return &config.ConfigRequirements{Rules: execpolicy.NewRequirementsPolicy(&execpolicy.Policy{Rules: []execpolicy.PrefixRule{{
		Pattern:       [][]string{{"rm"}},
		Decision:      execpolicy.DecisionPromptUser,
		Justification: "managed checkpoint",
	}}})}
}

// newManagedCheckpointRouter builds a router whose managed requirements hold a
// prompt-user rule for `rm`.
func newManagedCheckpointRouter(t *testing.T, configBody string) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	if configBody == "" {
		configBody = "approval_policy = \"on-request\"\n"
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{
		Requirements: managedCheckpointRequirements(),
	})
	t.Cleanup(func() { _ = router.Close() })
	params := &turn.TurnStartParams{ThreadID: "thread-1", CWD: cwd, Model: "gpt-test"}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	return router
}

func managedCheckpointShellRequest(t *testing.T) *tool.ShellApprovalRequest {
	t.Helper()
	return &tool.ShellApprovalRequest{
		Request:    &tool.ShellRequest{HookCommand: "rm -rf build", CWD: t.TempDir()},
		Invocation: &tool.Invocation{CallID: "call-1"},
	}
}

// TestManagedCheckpointRequiresCapableConnectionLikeRust pins Rust #58045: a
// prompt-user checkpoint goes to a requiredHumanApproval connection as a
// humanCommand with a unique approval id.
func TestManagedCheckpointRequiresCapableConnectionLikeRust(t *testing.T) {
	router := newManagedCheckpointRouter(t, "")
	requests := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(connectionServerRequestSink{
		connectionID: "conn-human",
		send:         func(request *ServerRequest) { requests <- request },
	})
	router.requireServerRequests().EnableHumanApprovalConnection("conn-human")

	decisionCh := make(chan tool.ShellApprovalDecision, 1)
	errCh := make(chan error, 1)
	go func() {
		decision, err := router.shellApprovalForTurn("thread-1", "turn-1", false)(context.Background(), managedCheckpointShellRequest(t))
		decisionCh <- decision
		errCh <- err
	}()

	var request *ServerRequest
	select {
	case request = <-requests:
	case <-time.After(2 * time.Second):
		t.Fatal("human approval request was not delivered")
	}
	params, ok := request.Params.(*CommandExecutionRequestApprovalParams)
	if !ok {
		t.Fatalf("params type = %T", request.Params)
	}
	if params.Kind != ExecApprovalKindHumanCommand {
		t.Fatalf("kind = %q, want humanCommand", params.Kind)
	}
	if params.ApprovalID == nil || strings.TrimSpace(*params.ApprovalID) == "" {
		t.Fatalf("approvalId = %#v, want a unique id", params.ApprovalID)
	}
	if params.ProposedExecPolicyAmendment != nil || params.NetworkApprovalContext != nil {
		t.Fatalf("checkpoint carried reusable authority: %#v", params)
	}
	if params.Reason == nil || !strings.Contains(*params.Reason, "requires human approval: managed checkpoint") {
		t.Fatalf("reason = %#v", params.Reason)
	}

	// A non-capable connection cannot answer the checkpoint.
	if resolved, _ := router.requireServerRequests().ResolveFromConnection("conn-other", OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: CommandExecutionApprovalAccept})); resolved {
		t.Fatal("non-capable connection resolved a managed checkpoint")
	}
	if _, err := router.requireServerRequests().ResolveFromConnection("conn-human", OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: CommandExecutionApprovalAccept})); err != nil {
		t.Fatalf("ResolveFromConnection() error = %v", err)
	}

	select {
	case decision := <-decisionCh:
		if err := <-errCh; err != nil {
			t.Fatalf("shell approval error = %v", err)
		}
		if !decision.Approved {
			t.Fatalf("decision = %#v, want approved", decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shell approval did not resolve")
	}
}

// TestManagedCheckpointRejectsWithoutCapableConnectionLikeRust pins the
// fail-closed path: no capable client means the command never runs.
func TestManagedCheckpointRejectsWithoutCapableConnectionLikeRust(t *testing.T) {
	router := newManagedCheckpointRouter(t, "")
	router.SetServerRequestSink(connectionServerRequestSink{
		connectionID: "conn-plain",
		send:         func(*ServerRequest) { t.Error("checkpoint was delivered to a non-capable connection") },
	})
	_, err := router.shellApprovalForTurn("thread-1", "turn-1", false)(context.Background(), managedCheckpointShellRequest(t))
	if err == nil || !strings.Contains(err.Error(), "no capable connection") {
		t.Fatalf("shell approval error = %v, want no-capable-connection", err)
	}
}

// TestManagedCheckpointIgnoresSessionApprovalLikeRust pins that a session-wide
// command approval cannot satisfy a managed checkpoint.
func TestManagedCheckpointIgnoresSessionApprovalLikeRust(t *testing.T) {
	router := newManagedCheckpointRouter(t, "")
	requests := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(connectionServerRequestSink{
		connectionID: "conn-human",
		send:         func(request *ServerRequest) { requests <- request },
	})
	router.requireServerRequests().EnableHumanApprovalConnection("conn-human")
	router.rememberCommandApprovalForSession("thread-1")

	go func() {
		_, _ = router.shellApprovalForTurn("thread-1", "turn-1", false)(context.Background(), managedCheckpointShellRequest(t))
	}()
	select {
	case request := <-requests:
		_, _ = router.requireServerRequests().ResolveFromConnection("conn-human", OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: CommandExecutionApprovalDecline}))
	case <-time.After(2 * time.Second):
		t.Fatal("session approval short-circuited a managed checkpoint")
	}
}

// TestManagedCheckpointOnlyAllowsOneTimeDecisionsLikeRust pins PendingApproval
// validation: reusable decisions are refused for a checkpoint.
func TestManagedCheckpointOnlyAllowsOneTimeDecisionsLikeRust(t *testing.T) {
	router := newManagedCheckpointRouter(t, "")
	requests := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(connectionServerRequestSink{
		connectionID: "conn-human",
		send:         func(request *ServerRequest) { requests <- request },
	})
	router.requireServerRequests().EnableHumanApprovalConnection("conn-human")

	decisions := make(chan tool.ShellApprovalDecision, 1)
	go func() {
		decision, _ := router.shellApprovalForTurn("thread-1", "turn-1", false)(context.Background(), managedCheckpointShellRequest(t))
		decisions <- decision
	}()
	request := <-requests
	_, _ = router.requireServerRequests().ResolveFromConnection("conn-human", OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: CommandExecutionApprovalAcceptForSession}))
	select {
	case decision := <-decisions:
		if decision.Approved || decision.DenyReason != "Managed command policy requires a one-time human approval." {
			t.Fatalf("decision = %#v", decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shell approval did not resolve")
	}
}

// TestManagedCheckpointRejectedUnderAutomaticReviewLikeRust pins the session
// pre-check: a checkpoint cannot be answered by an automatic reviewer.
func TestManagedCheckpointRejectedUnderAutomaticReviewLikeRust(t *testing.T) {
	router := newManagedCheckpointRouter(t, "approvals_reviewer = \"auto_review\"\n")
	decision, err := router.shellApprovalForTurn("thread-1", "turn-1", false)(context.Background(), managedCheckpointShellRequest(t))
	if err != nil {
		t.Fatalf("shell approval error = %v", err)
	}
	if decision.Approved || !strings.Contains(decision.DenyReason, "requires human approval, but this session requires automatic review") {
		t.Fatalf("decision = %#v", decision)
	}
}

// TestManagedCheckpointSegmentsEvaluateEveryCommandLikeRust pins the
// per-segment evaluation Rust performs before matching a rule.
func TestManagedCheckpointSegmentsEvaluateEveryCommandLikeRust(t *testing.T) {
	router := newManagedCheckpointRouter(t, "")
	reason, human := router.managedHumanApprovalCheckpoint("thread-1", "echo prepare && rm -rf build")
	if !human {
		t.Fatal("compound command did not require human approval")
	}
	if !strings.Contains(reason, "`rm -rf build`") {
		t.Fatalf("reason = %q", reason)
	}
	if _, human := router.managedHumanApprovalCheckpoint("thread-1", "echo rm -rf build"); human {
		t.Fatal("rule matched a non-command token")
	}
	if _, human := router.managedHumanApprovalCheckpoint("thread-1", "rm -rf build > /tmp/out"); human {
		t.Fatal("rule matched an unsupported redirection segment")
	}
}

// TestBrokerReplaysHumanApprovalOnlyToCapableConnectionsLikeRust pins the
// replay/recipient gate from Rust #58045.
func TestBrokerReplaysHumanApprovalOnlyToCapableConnectionsLikeRust(t *testing.T) {
	broker := NewServerRequestBroker()
	sent := make(chan string, 4)
	broker.SetSink(connectionServerRequestSink{connectionID: "conn-capable", send: func(*ServerRequest) { sent <- "conn-capable" }})
	broker.EnableHumanApprovalConnection("conn-capable")

	params := &CommandExecutionRequestApprovalParams{ThreadID: "thread-1", Kind: ExecApprovalKindHumanCommand}
	done := make(chan error, 1)
	go func() {
		var response CommandExecutionRequestApprovalResponse
		done <- broker.Request(context.Background(), ServerRequestCommandExecutionApproval, params, &response)
	}()

	var request *ServerRequest
	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Fatal("checkpoint was not delivered")
	}
	requests, _ := broker.pendingRequestsForThread("thread-1")
	if len(requests) != 1 {
		t.Fatalf("pending = %#v", requests)
	}
	request = requests[0]

	if replayed := broker.ReplayThreadToConnection("thread-1", "conn-plain"); replayed != 0 {
		t.Fatalf("replayed to non-capable connection = %d", replayed)
	}
	if replayed := broker.ReplayThreadToConnection("thread-1", "conn-capable"); replayed != 1 {
		t.Fatalf("replayed to capable connection = %d", replayed)
	}
	_, _ = broker.ResolveFromConnection("conn-capable", OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: CommandExecutionApprovalAccept}))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Request() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Request() did not resolve")
	}
}

// TestBrokerRejectsHumanApprovalWithoutCapableConnectionLikeRust pins the
// fail-closed delivery rule at the broker boundary.
func TestBrokerRejectsHumanApprovalWithoutCapableConnectionLikeRust(t *testing.T) {
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(*ServerRequest) { t.Error("checkpoint delivered without a capable connection") }))
	err := broker.Request(context.Background(), ServerRequestCommandExecutionApproval, &CommandExecutionRequestApprovalParams{Kind: ExecApprovalKindHumanCommand}, nil)
	if err == nil || !strings.Contains(err.Error(), "no capable connection") {
		t.Fatalf("Request() error = %v", err)
	}
	var missing interface{ Unwrap() error }
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v", err)
	}
}
