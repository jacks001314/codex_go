package appserver

import (
	"reflect"
	"testing"

	"codex_go/execpolicy"
	"codex_go/network"
)

// Mirrors Rust #60046: a saved denial in the exec policy file keeps winning
// when a later line re-grants the same host, so a stale grant cannot shadow it.
func TestApplyExecPolicyNetworkRulesKeepsSavedDenialOverLaterGrantLikeRust(t *testing.T) {
	home := t.TempDir()
	path := execpolicy.DefaultPolicyPath(home)
	if err := execpolicy.AppendNetworkRule(path, "blocked.example.com", "https", execpolicy.DecisionForbidden, "Deny https access to blocked.example.com"); err != nil {
		t.Fatalf("AppendNetworkRule(deny) error = %v", err)
	}
	if err := execpolicy.AppendNetworkRule(path, "blocked.example.com", "https", execpolicy.DecisionAllow, "Allow https access to blocked.example.com"); err != nil {
		t.Fatalf("AppendNetworkRule(allow) error = %v", err)
	}
	proxyConfig := &network.ProxyConfig{Network: network.DefaultProxySettings()}
	if err := applyExecPolicyNetworkRules(proxyConfig, home); err != nil {
		t.Fatalf("applyExecPolicyNetworkRules() error = %v", err)
	}
	if got := proxyConfig.Network.AllowedDomains(); got != nil {
		t.Fatalf("allowed domains = %v, want the saved denial to win", got)
	}
	if got := proxyConfig.Network.DeniedDomains(); !reflect.DeepEqual(got, []string{"blocked.example.com"}) {
		t.Fatalf("denied domains = %v", got)
	}
}
