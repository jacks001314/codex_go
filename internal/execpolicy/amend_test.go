package execpolicy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendNetworkRuleMatchesRustAndDeduplicates(t *testing.T) {
	policyPath := filepath.Join(t.TempDir(), "rules", "default.rules")
	for range 2 {
		if err := AppendNetworkRule(policyPath, "API.GitHub.com:443", "https_connect", DecisionAllow, "Allow https_connect access to api.github.com"); err != nil {
			t.Fatalf("AppendNetworkRule() error = %v", err)
		}
	}
	contents, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "network_rule(host=\"api.github.com\", protocol=\"https\", decision=\"allow\", justification=\"Allow https_connect access to api.github.com\")\n"
	if string(contents) != want {
		t.Fatalf("policy contents = %q, want %q", contents, want)
	}
	policy, err := LoadPolicies([]string{policyPath})
	if err != nil {
		t.Fatalf("LoadPolicies() error = %v", err)
	}
	if len(policy.NetworkRules) != 1 || policy.NetworkRules[0].Host != "api.github.com" || policy.NetworkRules[0].Protocol != "https" || policy.NetworkRules[0].Decision != DecisionAllow {
		t.Fatalf("network rules = %#v", policy.NetworkRules)
	}
}

func TestAppendNetworkRuleWritesRustDenySpelling(t *testing.T) {
	policyPath := DefaultPolicyPath(t.TempDir())
	if err := AppendNetworkRule(policyPath, "example.test", "socks5_udp", DecisionForbidden, "Deny socks5_udp access to example.test"); err != nil {
		t.Fatalf("AppendNetworkRule() error = %v", err)
	}
	contents, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "network_rule(host=\"example.test\", protocol=\"socks5_udp\", decision=\"deny\", justification=\"Deny socks5_udp access to example.test\")\n"
	if string(contents) != want {
		t.Fatalf("policy contents = %q, want %q", contents, want)
	}
}
