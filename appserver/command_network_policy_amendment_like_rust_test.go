package appserver

import (
	"context"
	"os"
	"sync/atomic"
	"testing"

	"codex_go/config"
	"codex_go/tool"
)

func commandNetworkPolicyHostDenied(router *RuntimeRouter, host string) bool {
	service := router.networkApproval
	if service == nil {
		return false
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	for key := range service.denied {
		if key.host == host {
			return true
		}
	}
	return false
}

func commandNetworkPolicyHostAllowed(router *RuntimeRouter, host string) bool {
	service := router.networkApproval
	if service == nil {
		return false
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	for key := range service.allowed {
		if key.host == host {
			return true
		}
	}
	return false
}

// TestCommandApprovalNetworkPolicyAmendmentPersistsAndPublishesLikeRust pins the
// command-approval half of the network-policy amendment: choosing
// `applyNetworkPolicyAmendment` on a shell approval must write the rule to the
// execpolicy file and publish it, instead of only answering this one command.
func TestCommandApprovalNetworkPolicyAmendmentPersistsAndPublishesLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("approval_policy = \"on_request\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), DefaultCWD: home})
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
		router.requireServerRequests().Resolve(OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: decision}))
	}))

	approval := router.shellApprovalForTurn("thread-cmd-net", "turn-cmd-net", false)
	decision, err := approval(context.Background(), &tool.ShellApprovalRequest{
		Request: &tool.ShellRequest{
			HookCommand:    "curl https://denied.example",
			CWD:            home,
			ApprovalReason: "requires approval",
		},
		Invocation: &tool.Invocation{CallID: "call-cmd-net"},
	})
	if err != nil {
		t.Fatalf("approval error = %v", err)
	}
	if decision.Approved {
		t.Fatalf("deny amendment approved the command: %#v", decision)
	}
	if prompts.Load() != 1 {
		t.Fatalf("prompts = %d, want 1", prompts.Load())
	}
	if !commandNetworkPolicyHostDenied(router, "denied.example") {
		t.Fatal("deny amendment was not published to the in-memory policy")
	}
	// The queued rule is host-level, so the published denial has to answer for
	// every protocol and port of the host, not only the request that produced
	// the amendment.
	if allow, denied := router.networkApproval.hostPolicyDecision("thread-cmd-net", "denied.example"); !denied || allow {
		t.Fatalf("host policy decision = allow:%t denied:%t, want the host denied for every port and protocol", allow, denied)
	}
	if allow, denied := router.networkApproval.hostPolicyDecision("thread-cmd-net", "allowed.example"); allow || denied {
		t.Fatalf("unrelated host decision = allow:%t denied:%t", allow, denied)
	}
	rules, err := loadExecPolicyNetworkRules(home)
	if err != nil {
		t.Fatalf("loadExecPolicyNetworkRules() error = %v", err)
	}
	found := false
	for _, rule := range rules {
		if rule.Host == "denied.example" {
			found = true
		}
	}
	if !found {
		t.Fatalf("execpolicy rules = %#v, want the denied host", rules)
	}

	// The allow branch publishes only after a successful write.
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		decision := map[string]any{
			string(CommandExecutionApprovalApplyNetworkPolicyAmendment): map[string]any{
				"network_policy_amendment": map[string]any{
					"host":   "allowed.example",
					"action": string(NetworkPolicyRuleAllow),
				},
			},
		}
		router.requireServerRequests().Resolve(OK(request.ID, &CommandExecutionRequestApprovalResponse{Decision: decision}))
	}))
	allowDecision, err := approval(context.Background(), &tool.ShellApprovalRequest{
		Request: &tool.ShellRequest{
			HookCommand:    "curl https://allowed.example",
			CWD:            home,
			ApprovalReason: "requires approval",
		},
		Invocation: &tool.Invocation{CallID: "call-cmd-net-allow"},
	})
	if err != nil {
		t.Fatalf("allow approval error = %v", err)
	}
	if !allowDecision.Approved {
		t.Fatalf("allow amendment did not approve the command: %#v", allowDecision)
	}
	if !commandNetworkPolicyHostAllowed(router, "allowed.example") {
		t.Fatal("allow amendment was not published after the write")
	}
}
