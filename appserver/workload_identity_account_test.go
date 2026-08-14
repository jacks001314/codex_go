package appserver

import (
	"testing"

	"codex_go/auth"
)

// TestAccountRPCsRejectWorkloadIdentityLikeRust mirrors Rust #38426: the
// host-owned workload identity session cannot be changed through the account
// login/logout RPCs, even though an explicit API key may coexist.
func TestAccountRPCsRejectWorkloadIdentityLikeRust(t *testing.T) {
	t.Setenv(auth.OpenAIFederationRuleIDEnv, "rule-1")
	t.Setenv(auth.OpenAIIdentityTokenFileEnv, "C:/assertion.jwt")

	router := &RuntimeRouter{}
	if _, err := router.handleLoginAccount(&Request{}); err == nil {
		t.Fatalf("handleLoginAccount accepted workload-identity selection")
	}
	if _, err := router.handleLogoutAccount(&Request{}); err == nil {
		t.Fatalf("handleLogoutAccount accepted workload-identity selection")
	}
}
