package appserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"codex_go/network"
	"codex_go/tool"
)

// TestNetworkApprovalDenialSurvivesProcessFinishDuringPersistLikeRust pins Rust
// #42746: an explicit network-policy denial is stored before waiting on the
// execpolicy commit, so a process that finishes while persistence is still in
// flight keeps the denial as its call outcome instead of reporting success.
func TestNetworkApprovalDenialSurvivesProcessFinishDuringPersistLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	service := router.networkApproval
	invocation := &tool.Invocation{CallID: "exec-network-persist", ToolName: tool.PlainName("exec_command")}
	service.registerActiveCall("thread-network", "turn-network", invocation)

	persistStarted := make(chan struct{})
	releasePersist := make(chan struct{})
	var once sync.Once
	service.persistAmendment = func(*NetworkPolicyAmendment, NetworkApprovalProtocol) error {
		once.Do(func() { close(persistStarted) })
		<-releasePersist
		return nil
	}
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		decision := map[string]any{
			string(CommandExecutionApprovalApplyNetworkPolicyAmendment): map[string]any{
				"network_policy_amendment": map[string]any{"host": "pending.example", "action": string(NetworkPolicyRuleDeny)},
			},
		}
		_, _ = router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": decision}})
	}))

	decided := make(chan network.ProxyDecision, 1)
	go func() {
		decided <- service.Decide(context.Background(), network.ProxyPolicyRequest{
			Protocol:      network.ProxyProtocolHTTPSConnect,
			Host:          "pending.example",
			Port:          443,
			EnvironmentID: "local",
		})
	}()

	select {
	case <-persistStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("network policy persistence did not start")
	}

	if outcome := service.finishActiveCall("thread-network", "turn-network", invocation); outcome != "rejected by user" {
		t.Fatalf("call outcome during persistence = %q, want the explicit denial", outcome)
	}
	close(releasePersist)
	select {
	case decision := <-decided:
		if decision.Allow || decision.Decision != network.ProxyPolicyDecisionDeny {
			t.Fatalf("decision = %#v, want deny", decision)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("network approval did not resolve")
	}
}
