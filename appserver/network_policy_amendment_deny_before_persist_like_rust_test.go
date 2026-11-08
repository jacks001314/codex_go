package appserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"codex_go/network"
	"codex_go/tool"
)

// TestNetworkPolicyAmendmentAppliesDenyBeforePersistLikeRust pins Rust
// core/src/exec_policy.rs:566-640 `apply_network_rule_and_persist`: a Forbidden
// amendment is published to the in-memory policy (and the proxy denylist)
// before the policy file is written, so a failed write still leaves the denial
// in force for later requests instead of re-prompting or falling back to an
// older grant.
func denied(decision network.ProxyDecision) bool {
	return !decision.Allow
}

func allowed(decision network.ProxyDecision) bool {
	return decision.Allow
}

func TestNetworkPolicyAmendmentAppliesDenyBeforePersistLikeRust(t *testing.T) {
	router := newNetworkApprovalTestRouter(t, "on-request")
	service := router.networkApproval
	invocation := &tool.Invocation{CallID: "exec-network-deny-persist", ToolName: tool.PlainName("exec_command")}
	service.registerActiveCall("thread-network", "turn-network", invocation)

	service.persistAmendment = func(*NetworkPolicyAmendment, NetworkApprovalProtocol) error {
		return errors.New("policy file is read-only")
	}
	var prompts atomic.Int32
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		prompts.Add(1)
		decision := map[string]any{
			string(CommandExecutionApprovalApplyNetworkPolicyAmendment): map[string]any{
				"network_policy_amendment": map[string]any{
					"host":   "denied.example",
					"action": string(NetworkPolicyRuleDeny),
				},
			},
		}
		_, _ = router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": decision}})
	}))

	request := network.ProxyPolicyRequest{
		Protocol:      network.ProxyProtocolHTTPSConnect,
		Host:          "denied.example",
		Port:          443,
		EnvironmentID: "local",
	}
	if decision := service.Decide(context.Background(), request); !denied(decision) {
		t.Fatalf("first decision = %#v, want deny", decision)
	}
	// The denial must already be in memory, so the next request for the same host
	// is denied without another prompt even though the write failed.
	if decision := service.Decide(context.Background(), request); !denied(decision) {
		t.Fatalf("second decision = %#v, want deny", decision)
	}
	if got := prompts.Load(); got != 1 {
		t.Fatalf("prompts = %d, want 1: the denial was not published before persistence", got)
	}

	// A grant is published only after the write succeeds.
	service.persistAmendment = func(*NetworkPolicyAmendment, NetworkApprovalProtocol) error { return nil }
	allowRequest := network.ProxyPolicyRequest{
		Protocol:      network.ProxyProtocolHTTPSConnect,
		Host:          "allowed.example",
		Port:          443,
		EnvironmentID: "local",
	}
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		prompts.Add(1)
		decision := map[string]any{
			string(CommandExecutionApprovalApplyNetworkPolicyAmendment): map[string]any{
				"network_policy_amendment": map[string]any{
					"host":   "allowed.example",
					"action": string(NetworkPolicyRuleAllow),
				},
			},
		}
		_, _ = router.requireServerRequests().Resolve(&Response{ID: request.ID, Result: map[string]any{"decision": decision}})
	}))
	if decision := service.Decide(context.Background(), allowRequest); !allowed(decision) {
		t.Fatalf("allow decision = %#v, want allow", decision)
	}
	if decision := service.Decide(context.Background(), allowRequest); !allowed(decision) {
		t.Fatalf("reused allow decision = %#v, want allow", decision)
	}
	if got := prompts.Load(); got != 2 {
		t.Fatalf("prompts = %d, want 2: the persisted grant was not published", got)
	}
}
