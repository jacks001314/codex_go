package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/network"
)

// Rust #63365: interrupting exec_command while startup is pending retires its
// network approval registration. The next command's untagged proxy request must
// prompt for approval instead of being attributed to the ended execution and
// denied outright, and every deferred observer still reports the cached denial.
func TestNetworkApprovalInterruptedStartupRetiresRegistrationLikeRust(t *testing.T) {
	const (
		threadID = "thread-network"
		turnID   = "turn-network"
		callID   = "execution-interrupted-startup"
	)
	router := newNetworkApprovalTestRouter(t, "on-request")
	service := router.networkApproval
	toolCtx, cancelTool := context.WithCancel(context.Background())
	defer cancelTool()
	invocation := registerOriginatingExecutionCall(t, router, toolCtx, threadID, turnID, callID, router.services.DefaultCWD, "local")
	if call := service.activeCallForApproval(router.activeTurnForNetworkApprovalThread(threadID, "local"), "local", callID); call == nil {
		t.Fatal("execution was not registered")
	}
	service.recordUserDenialForThread(threadID)

	cancelTool()
	deadline := time.Now().Add(5 * time.Second)
	for {
		service.callsMu.Lock()
		stale := service.singleActiveCallLocked(threadID)
		service.callsMu.Unlock()
		if stale == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("interrupted startup kept its network approval registration")
		}
		time.Sleep(10 * time.Millisecond)
	}

	requests := make(chan *ServerRequest, 1)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		select {
		case requests <- request:
		default:
		}
	}))
	result := make(chan network.ProxyDecision, 1)
	go func() {
		result <- service.decideForThread(context.Background(), threadID, network.ProxyPolicyRequest{
			Protocol:      network.ProxyProtocolHTTP,
			Host:          "example.com",
			Port:          80,
			Method:        "GET",
			EnvironmentID: "local",
		})
	}()
	var prompt *ServerRequest
	select {
	case prompt = <-requests:
	case <-time.After(5 * time.Second):
		t.Fatal("untagged request for the next command was denied without an approval prompt")
	}
	_, _ = router.requireServerRequests().Resolve(&Response{
		ID:     prompt.ID,
		Result: map[string]any{"decision": string(CommandExecutionApprovalDecline)},
	})
	select {
	case decision := <-result:
		if decision.Allow {
			t.Fatalf("decision = %#v, want denied", decision)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("untagged request did not resolve")
	}

	if outcome := service.finishActiveCall(threadID, turnID, invocation); outcome != "rejected by user" {
		t.Fatalf("deferred outcome = %q, want the interrupted denial", outcome)
	}
	if again := service.finishActiveCall(threadID, turnID, invocation); again != "" {
		t.Fatalf("second observer outcome = %q, want the cached outcome consumed once", again)
	}
}

// A persistent execution keeps its registration until its process ends, so the
// startup cleanup must not retire a background command that outlives the call.
func TestNetworkApprovalInterruptedStartupKeepsPersistentExecutionLikeRust(t *testing.T) {
	const (
		threadID = "thread-network"
		turnID   = "turn-network"
		callID   = "execution-persistent-startup"
	)
	router := newNetworkApprovalTestRouter(t, "on-request")
	service := router.networkApproval
	toolCtx, cancelTool := context.WithCancel(context.Background())
	defer cancelTool()
	invocation := registerOriginatingExecutionCall(t, router, toolCtx, threadID, turnID, callID, router.services.DefaultCWD, "local")
	service.markExecutionPersistent(threadID, turnID, callID)
	cancelTool()
	time.Sleep(100 * time.Millisecond)
	if call := service.activeCallForApproval(router.activeTurnForNetworkApprovalThread(threadID, "local"), "local", callID); call == nil {
		t.Fatal("persistent execution lost its registration after the tool call ended")
	}
	service.finishExecution(threadID, callID, "process exited")
	_ = service.finishActiveCall(threadID, turnID, invocation)
	service.callsMu.Lock()
	remaining := len(service.executions)
	service.callsMu.Unlock()
	if remaining != 0 {
		t.Fatalf("executions = %d, want the finished process dropped", remaining)
	}
}
