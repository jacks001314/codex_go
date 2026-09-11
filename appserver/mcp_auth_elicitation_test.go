package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

// TestMCPElicitationsAllowedForApprovalLikeRust covers Rust's approval-policy
// gate for agent-initiated MCP elicitations (maybe_request_codex_apps_auth_elicitation).
func TestMCPElicitationsAllowedForApprovalLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	if router.mcpElicitationsAllowedForApproval(nil, sandbox.ApprovalNever, nil) {
		t.Fatal("never must not allow MCP elicitations")
	}
	for _, policy := range []sandbox.AskForApproval{sandbox.ApprovalOnRequest, sandbox.ApprovalUnlessTrusted} {
		if !router.mcpElicitationsAllowedForApproval(nil, policy, nil) {
			t.Fatalf("%s must allow MCP elicitations", policy)
		}
	}
	// Granular requires the granular configuration to allow them.
	if router.mcpElicitationsAllowedForApproval(nil, sandbox.ApprovalGranular, nil) {
		t.Fatal("granular without a configuration must not allow MCP elicitations")
	}
	allowed := &config.Config{Values: map[string]any{
		"approval_policy": map[string]any{"granular": map[string]any{"mcp_elicitations": true}},
	}}
	if !router.mcpElicitationsAllowedForApproval(allowed, sandbox.ApprovalGranular, nil) {
		t.Fatal("granular with mcp_elicitations=true must allow MCP elicitations")
	}
	denied := &config.Config{Values: map[string]any{
		"approval_policy": map[string]any{"granular": map[string]any{"mcp_elicitations": false}},
	}}
	if router.mcpElicitationsAllowedForApproval(denied, sandbox.ApprovalGranular, nil) {
		t.Fatal("granular with mcp_elicitations=false must not allow MCP elicitations")
	}
}
