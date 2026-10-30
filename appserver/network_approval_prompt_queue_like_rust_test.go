package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/network"
	"codex_go/tool"
)

func registerPromptQueueExecution(t *testing.T, router *RuntimeRouter, callID string) {
	t.Helper()
	router.networkApproval.registerActiveCall("thread-network", "turn-network", &tool.Invocation{
		CallID:   callID,
		ToolName: tool.PlainName("exec_command"),
	})
}

func promptQueuePolicyRequest(callID string, port uint16) network.ProxyPolicyRequest {
	return network.ProxyPolicyRequest{
		Protocol:      network.ProxyProtocolHTTP,
		Host:          "example.test",
		Port:          port,
		EnvironmentID: "local",
		ExecutionID:   callID,
		Method:        "GET",
	}
}

func nextPromptRequest(t *testing.T, requests <-chan *ServerRequest) *ServerRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(5 * time.Second):
		t.Fatal("network approval prompt did not arrive")
		return nil
	}
}

func resolveNetworkPrompt(t *testing.T, router *RuntimeRouter, request *ServerRequest, decision CommandExecutionApprovalDecision, wantResolved bool) {
	t.Helper()
	resolved, err := router.requireServerRequests().Resolve(&Response{
		ID: request.ID, Result: map[string]any{"decision": string(decision)},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved != wantResolved {
		t.Fatalf("Resolve() = %t, want %t", resolved, wantResolved)
	}
}

// Rust #58351: concurrent executions for one endpoint show a single manual
// prompt; the queued execution prompts only after a one-time decision.
func TestNetworkApprovalQueuesManualPromptsPerEndpointLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	registerPromptQueueExecution(t, router, "exec-a")
	registerPromptQueueExecution(t, router, "exec-b")
	requests := make(chan *ServerRequest, 4)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) { requests <- request }))

	results := make(chan network.ProxyDecision, 2)
	go func() {
		results <- router.networkApproval.Decide(context.Background(), promptQueuePolicyRequest("exec-a", 80))
	}()
	first := nextPromptRequest(t, requests)
	go func() {
		results <- router.networkApproval.Decide(context.Background(), promptQueuePolicyRequest("exec-b", 80))
	}()
	select {
	case duplicate := <-requests:
		t.Fatalf("duplicate prompt before the first decision: %#v", duplicate.Params)
	case <-time.After(50 * time.Millisecond):
	}
	resolveNetworkPrompt(t, router, first, CommandExecutionApprovalAccept, true)
	second := nextPromptRequest(t, requests)
	resolveNetworkPrompt(t, router, second, CommandExecutionApprovalAccept, true)
	for range 2 {
		if decision := waitNetworkApprovalDecision(t, results); !decision.Allow {
			t.Fatalf("decision = %#v", decision)
		}
	}
}

// Rust #58351: a queued execution reuses a committed session grant instead of
// prompting again.
func TestNetworkApprovalQueuedExecutionReusesSessionGrantLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	registerPromptQueueExecution(t, router, "exec-a")
	registerPromptQueueExecution(t, router, "exec-b")
	requests := make(chan *ServerRequest, 4)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) { requests <- request }))

	results := make(chan network.ProxyDecision, 2)
	go func() {
		results <- router.networkApproval.Decide(context.Background(), promptQueuePolicyRequest("exec-a", 80))
	}()
	resolveNetworkPrompt(t, router, nextPromptRequest(t, requests), CommandExecutionApprovalAcceptForSession, true)
	go func() {
		results <- router.networkApproval.Decide(context.Background(), promptQueuePolicyRequest("exec-b", 80))
	}()
	for range 2 {
		if decision := waitNetworkApprovalDecision(t, results); !decision.Allow {
			t.Fatalf("decision = %#v", decision)
		}
	}
	select {
	case duplicate := <-requests:
		t.Fatalf("session grant prompted again: %#v", duplicate.Params)
	case <-time.After(50 * time.Millisecond):
	}
}

// Rust #58351: cancelling a queued execution neither prompts nor disturbs the
// execution that owns the visible prompt.
func TestNetworkApprovalQueuedCancellationDoesNotPromptLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	registerPromptQueueExecution(t, router, "exec-a")
	registerPromptQueueExecution(t, router, "exec-b")
	requests := make(chan *ServerRequest, 4)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) { requests <- request }))

	owner := make(chan network.ProxyDecision, 1)
	go func() {
		owner <- router.networkApproval.Decide(context.Background(), promptQueuePolicyRequest("exec-a", 80))
	}()
	first := nextPromptRequest(t, requests)

	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan network.ProxyDecision, 1)
	go func() { queued <- router.networkApproval.Decide(ctx, promptQueuePolicyRequest("exec-b", 80)) }()
	cancel()
	if decision := waitNetworkApprovalDecision(t, queued); decision.Allow {
		t.Fatalf("cancelled queued decision = %#v", decision)
	}
	select {
	case duplicate := <-requests:
		t.Fatalf("cancelled execution prompted: %#v", duplicate.Params)
	case <-time.After(50 * time.Millisecond):
	}
	resolveNetworkPrompt(t, router, first, CommandExecutionApprovalAccept, true)
	if decision := waitNetworkApprovalDecision(t, owner); !decision.Allow {
		t.Fatalf("owner decision = %#v", decision)
	}
}

// Rust #58351: queues are per endpoint, so an independent port prompts
// independently.
func TestNetworkApprovalPromptQueuesArePerEndpointLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	registerPromptQueueExecution(t, router, "exec-a")
	registerPromptQueueExecution(t, router, "exec-b")
	requests := make(chan *ServerRequest, 4)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) { requests <- request }))

	results := make(chan network.ProxyDecision, 2)
	go func() {
		results <- router.networkApproval.Decide(context.Background(), promptQueuePolicyRequest("exec-a", 80))
	}()
	first := nextPromptRequest(t, requests)
	go func() {
		results <- router.networkApproval.Decide(context.Background(), promptQueuePolicyRequest("exec-b", 8080))
	}()
	second := nextPromptRequest(t, requests)
	resolveNetworkPrompt(t, router, first, CommandExecutionApprovalAccept, true)
	resolveNetworkPrompt(t, router, second, CommandExecutionApprovalAccept, true)
	for range 2 {
		if decision := waitNetworkApprovalDecision(t, results); !decision.Allow {
			t.Fatalf("decision = %#v", decision)
		}
	}
}

// Rust #58350: when the proxy request disappears, its prompt is withdrawn, so a
// late response can neither apply a decision nor interrupt the turn.
func TestNetworkApprovalDisconnectedRequestWithdrawsPromptLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	registerPromptQueueExecution(t, router, "exec-a")
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	requests := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) { requests <- request }))

	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan network.ProxyDecision, 1)
	go func() { results <- router.networkApproval.Decide(ctx, promptQueuePolicyRequest("exec-a", 80)) }()
	request := nextPromptRequest(t, requests)
	cancel()
	if decision := waitNetworkApprovalDecision(t, results); decision.Allow {
		t.Fatalf("disconnected request decision = %#v", decision)
	}
	resolveNetworkPrompt(t, router, request, CommandExecutionApprovalAccept, false)

	deadline := time.Now().Add(2 * time.Second)
	for {
		resolved := false
		for _, notification := range sink.List() {
			if notification.Method == NotificationServerRequestResolved {
				resolved = true
			}
		}
		if resolved {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serverRequest/resolved was not emitted for the withdrawn network prompt")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
