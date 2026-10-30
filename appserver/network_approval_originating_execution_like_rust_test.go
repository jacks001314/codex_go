package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/network"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

func newOriginatingExecutionNetworkRouter(t *testing.T, approvalsReviewer string) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	body := "approval_policy = \"on-request\"\nsandbox_mode = \"workspace-write\"\n"
	if approvalsReviewer != "" {
		body += "approvals_reviewer = \"" + approvalsReviewer + "\"\n"
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), DefaultCWD: home})
	t.Cleanup(func() { _ = router.Close() })
	return router
}

func startOriginatingExecutionTurn(t *testing.T, router *RuntimeRouter, threadID string, turnID string, cwd string, model string, reviewer string) {
	t.Helper()
	params := &turn.TurnStartParams{
		ThreadID:     threadID,
		CWD:          cwd,
		Environments: []map[string]any{{"environmentId": "local", "cwd": cwd}},
	}
	if strings.TrimSpace(reviewer) != "" {
		value := reviewer
		params.ApprovalsReviewer = &value
	}
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, 1, params); err != nil {
		t.Fatal(err)
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "connection-network", &appTurnRunConfig{
		Model: model, Originator: "codex-test", ApprovalPolicy: "on-request",
	})
}

func registerOriginatingExecutionCall(t *testing.T, router *RuntimeRouter, ctx context.Context, threadID string, turnID string, callID string, cwd string, environmentID string) *tool.Invocation {
	t.Helper()
	arguments, err := json.Marshal(tool.ExecCommandArgs{
		Cmd: "curl https://example.com", CWD: cwd, EnvironmentID: environmentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	invocation := &tool.Invocation{
		CallID:   callID,
		ToolName: tool.PlainName("exec_command"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: string(arguments)},
	}
	router.networkApproval.registerActiveCallWithContext(ctx, threadID, turnID, invocation)
	return invocation
}

func originatingExecutionPolicyRequest(callID string, environmentID string) network.ProxyPolicyRequest {
	return network.ProxyPolicyRequest{
		Protocol: network.ProxyProtocolHTTP, Host: "example.com", Port: 80,
		EnvironmentID: environmentID, ExecutionID: callID, Method: "GET",
	}
}

func waitNetworkApprovalDecision(t *testing.T, result <-chan network.ProxyDecision) network.ProxyDecision {
	t.Helper()
	select {
	case decision := <-result:
		return decision
	case <-time.After(5 * time.Second):
		t.Fatal("network approval did not resolve")
		return network.ProxyDecision{}
	}
}

// Rust #44575: a background process reviews under the current turn's settings
// but the environment it started in. A later turn's cwd must not move the
// approval prompt away from the originating execution.
func TestNetworkApprovalUsesOriginatingExecutionCWDLikeRust(t *testing.T) {
	const (
		threadID = "thread-originating-cwd"
		callID   = "execution-originating-cwd"
	)
	router := newOriginatingExecutionNetworkRouter(t, "user")
	originalCWD := filepath.Join(t.TempDir(), "original")
	laterCWD := filepath.Join(t.TempDir(), "later")
	for _, dir := range []string{originalCWD, laterCWD} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	startOriginatingExecutionTurn(t, router, threadID, "turn-a", originalCWD, "model-a", "user")
	registerOriginatingExecutionCall(t, router, context.Background(), threadID, "turn-a", callID, originalCWD, "remote")
	router.networkApproval.markExecutionPersistent(threadID, "turn-a", callID)
	router.clearActiveRuntimeTurn(threadID, "turn-a")
	startOriginatingExecutionTurn(t, router, threadID, "turn-b", laterCWD, "model-b", "user")
	t.Cleanup(func() { router.clearActiveRuntimeTurn(threadID, "turn-b") })

	var approvalCWD string
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		params, ok := request.Params.(*CommandExecutionRequestApprovalParams)
		if !ok {
			t.Errorf("approval params = %T", request.Params)
			return
		}
		if params.CWD != nil {
			approvalCWD = *params.CWD
		}
		if params.TurnID != "turn-b" {
			t.Errorf("approval turn = %q, want the serviced turn", params.TurnID)
		}
		_, _ = router.requireServerRequests().Resolve(&Response{
			ID: request.ID, Result: map[string]any{"decision": string(CommandExecutionApprovalAccept)},
		})
	}))

	decision := router.networkApproval.decideForThread(context.Background(), threadID, originatingExecutionPolicyRequest(callID, "remote"))
	if !decision.Allow {
		t.Fatalf("decision = %#v, want allowed under the originating execution", decision)
	}
	if approvalCWD != originalCWD {
		t.Fatalf("approval cwd = %q, want the originating execution cwd %q", approvalCWD, originalCWD)
	}
}

// Rust #44575: Guardian receives the current review settings together with the
// originating execution's environment snapshot, not the later turn's world state.
func TestGuardianNetworkApprovalUsesOriginatingExecutionEnvironmentsLikeRust(t *testing.T) {
	const (
		threadID = "thread-originating-guardian"
		callID   = "execution-originating-guardian"
	)
	router := newOriginatingExecutionNetworkRouter(t, "auto_review")
	originalCWD := filepath.Join(t.TempDir(), "original")
	laterCWD := filepath.Join(t.TempDir(), "later")
	for _, dir := range []string{originalCWD, laterCWD} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	startOriginatingExecutionTurn(t, router, threadID, "turn-a", originalCWD, "model-a", "auto_review")
	registerOriginatingExecutionCall(t, router, context.Background(), threadID, "turn-a", callID, originalCWD, "local")
	router.networkApproval.markExecutionPersistent(threadID, "turn-a", callID)
	router.clearActiveRuntimeTurn(threadID, "turn-a")
	startOriginatingExecutionTurn(t, router, threadID, "turn-b", laterCWD, "model-b", "auto_review")
	t.Cleanup(func() { router.clearActiveRuntimeTurn(threadID, "turn-b") })

	var action state.Action
	var origin *guardianReviewOrigin
	router.services.GuardianReviewer = networkGuardianReviewerFunc(func(ctx context.Context, _ string, _ string, _ string, reviewed state.Action) (state.ReviewDecision, string, error) {
		action = reviewed
		origin = guardianReviewOriginFromContext(ctx)
		return state.DecisionApproved, "ok", nil
	})
	decision := router.networkApproval.decideForThread(context.Background(), threadID, originatingExecutionPolicyRequest(callID, "local"))
	if !decision.Allow {
		t.Fatalf("decision = %#v, want allowed", decision)
	}
	if action.CWD != originalCWD {
		t.Fatalf("guardian action cwd = %q, want %q", action.CWD, originalCWD)
	}
	trigger, ok := action.Extra["trigger"].(guardianNetworkAccessTrigger)
	if !ok || trigger.CWD != originalCWD {
		t.Fatalf("guardian trigger = %#v, want originating cwd %q", action.Extra["trigger"], originalCWD)
	}
	if origin == nil || origin.CWD != originalCWD {
		t.Fatalf("guardian review origin = %#v, want cwd %q", origin, originalCWD)
	}
	if plan := router.guardianReviewPlanForTurn(threadID, "turn-b"); plan.PrepareInput.ParentModel != "model-b" {
		t.Fatalf("review parent model = %q, want the current turn's model-b", plan.PrepareInput.ParentModel)
	}
	items, err := router.guardianEnvironmentInputItemsForOrigin(context.Background(), threadID, "turn-b", origin)
	if err != nil {
		t.Fatalf("environment items error = %v", err)
	}
	rendered := fmt.Sprint(items)
	if !strings.Contains(rendered, originalCWD) || strings.Contains(rendered, laterCWD) {
		t.Fatalf("guardian environment = %q, want the originating execution environment", rendered)
	}
}

// Rust #44575: an execution ending while its review is pending resolves the
// approval as denied, keeps the original call outcome, removes the pending
// approval, and does not grant the host for a late callback.
func TestNetworkApprovalCancellationDuringReviewLikeRust(t *testing.T) {
	const (
		threadID = "thread-network"
		turnID   = "turn-network"
		callID   = "execution-cancelled-during-review"
	)
	router := newNetworkApprovalTestRouter(t, "on-request")
	service := router.networkApproval
	execCtx, execCancel := context.WithCancelCause(context.Background())
	invocation := registerOriginatingExecutionCall(t, router, execCtx, threadID, turnID, callID, router.services.DefaultCWD, "local")
	service.markExecutionPersistent(threadID, turnID, callID)

	requests := make(chan *ServerRequest, 1)
	var prompts atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		prompts.Add(1)
		requests <- request
	}))

	call := service.activeCallForApproval(router.activeTurnForNetworkApprovalThread(threadID, "local"), "local", callID)
	if call == nil {
		t.Fatal("active execution not registered")
	}
	service.recordGuardianOutcome(call, "original execution denial")
	result := make(chan network.ProxyDecision, 1)
	go func() {
		result <- service.decideForThread(context.Background(), threadID, originatingExecutionPolicyRequest(callID, "local"))
	}()
	var request *ServerRequest
	select {
	case request = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("approval prompt was not requested")
	}

	service.finishExecution(threadID, callID, "process exited")
	decision := waitNetworkApprovalDecision(t, result)
	if decision.Allow {
		t.Fatalf("decision = %#v, want denied after execution cancellation", decision)
	}
	execCancel(errors.New("test cleanup"))
	service.mu.Lock()
	pending := len(service.pending)
	_, granted := service.allowed[networkApprovalKey{
		threadID: threadID, environmentID: "local", protocol: network.ProxyProtocolHTTP, host: "example.com", port: 80,
	}]
	service.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending approvals = %d, want 0", pending)
	}
	if granted {
		t.Fatal("cancelled review installed a session host grant")
	}
	if outcome := service.finishActiveCall(threadID, turnID, invocation); outcome != "original execution denial" {
		t.Fatalf("call outcome = %q, want the original execution denial", outcome)
	}

	// A late proxy callback must not resurrect the cancelled execution, and the
	// late user answer must not prompt again or install a grant.
	_, _ = router.requireServerRequests().Resolve(&Response{
		ID: request.ID, Result: map[string]any{"decision": string(CommandExecutionApprovalAcceptForSession)},
	})
	late := service.decideForThread(context.Background(), threadID, originatingExecutionPolicyRequest(callID, "local"))
	if late.Allow {
		t.Fatalf("late callback = %#v, want denied", late)
	}
	if prompts.Load() != 1 {
		t.Fatalf("approval prompts = %d, want only the cancelled review", prompts.Load())
	}
	service.mu.Lock()
	_, granted = service.allowed[networkApprovalKey{
		threadID: threadID, environmentID: "local", protocol: network.ProxyProtocolHTTP, host: "example.com", port: 80,
	}]
	service.mu.Unlock()
	if granted {
		t.Fatal("late callback installed a session host grant")
	}
}

// Rust #44575: cancellation after an accepted review keeps the accepted decision
// and its session grant. The cancelled execution is still denied on a late
// callback, while a new execution may reuse the grant.
func TestNetworkApprovalAcceptedReviewSurvivesCancellationLikeRust(t *testing.T) {
	const (
		threadID = "thread-network"
		turnID   = "turn-network"
		callID   = "execution-accepted"
	)
	router := newNetworkApprovalTestRouter(t, "on-request")
	service := router.networkApproval
	execCtx, execCancel := context.WithCancelCause(context.Background())
	invocation := registerOriginatingExecutionCall(t, router, execCtx, threadID, turnID, callID, router.services.DefaultCWD, "local")
	var prompts atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		prompts.Add(1)
		_, _ = router.requireServerRequests().Resolve(&Response{
			ID: request.ID, Result: map[string]any{"decision": string(CommandExecutionApprovalAcceptForSession)},
		})
	}))

	if decision := service.decideForThread(context.Background(), threadID, originatingExecutionPolicyRequest(callID, "local")); !decision.Allow {
		t.Fatalf("decision = %#v, want allowed", decision)
	}
	key := networkApprovalKey{
		threadID: threadID, environmentID: "local", protocol: network.ProxyProtocolHTTP, host: "example.com", port: 80,
	}
	service.mu.Lock()
	_, granted := service.allowed[key]
	service.mu.Unlock()
	if !granted {
		t.Fatal("accepted session review did not install its grant")
	}
	call := service.activeCallForApproval(router.activeTurnForNetworkApprovalThread(threadID, "local"), "local", callID)
	service.recordGuardianOutcome(call, "original execution denial")
	execCancel(errors.New("process exited"))

	if late := service.decideForThread(context.Background(), threadID, originatingExecutionPolicyRequest(callID, "local")); late.Allow {
		t.Fatalf("late callback = %#v, want denied for the cancelled execution", late)
	}
	if outcome := service.finishActiveCall(threadID, turnID, invocation); outcome != "original execution denial" {
		t.Fatalf("call outcome = %q, want the original execution denial", outcome)
	}
	service.mu.Lock()
	_, granted = service.allowed[key]
	service.mu.Unlock()
	if !granted {
		t.Fatal("execution cancellation revoked the accepted session grant")
	}

	newInvocation := registerOriginatingExecutionCall(t, router, context.Background(), threadID, turnID, "execution-reusing-grant", router.services.DefaultCWD, "local")
	if decision := service.decideForThread(context.Background(), threadID, originatingExecutionPolicyRequest("execution-reusing-grant", "local")); !decision.Allow {
		t.Fatalf("new execution decision = %#v, want the accepted grant reused", decision)
	}
	if prompts.Load() != 1 {
		t.Fatalf("approval prompts = %d, want the accepted session grant to avoid a new prompt", prompts.Load())
	}
	_ = service.finishActiveCall(threadID, turnID, newInvocation)
}
