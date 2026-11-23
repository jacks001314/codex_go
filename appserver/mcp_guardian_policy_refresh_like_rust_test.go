package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

// Rust #62276: an MCP configuration refresh preserves the active Guardian
// policy, so a policy published while MCP was reloading still constrains the
// next turn.
func TestMCPConfigRefreshKeepsPublishedGuardianPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	writeMCPConfigRefreshTestConfig(t, home, "")
	requirements := "allowed_approval_policies = [\"on-request\"]\n"
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	router := newMCPConfigRefreshTestRouter(home)
	policy := "Managed Guardian policy"
	router.services.Config.PublishGuardianPolicyRequirements(&config.ConfigRequirements{
		AllowedApprovalsReviewers: []config.ApprovalsReviewer{config.ApprovalsReviewerUser},
		GuardianPolicyConfig:      &policy,
	})

	if err := router.reloadMCPConfigStrict(); err != nil {
		t.Fatalf("reload MCP config error = %v", err)
	}

	got := router.services.Config.Requirements().Requirements
	if got == nil {
		t.Fatal("requirements = nil after MCP refresh")
	}
	if len(got.AllowedApprovalsReviewers) != 1 || got.AllowedApprovalsReviewers[0] != config.ApprovalsReviewerUser {
		t.Fatalf("allowed approvals reviewers = %#v, want [user]", got.AllowedApprovalsReviewers)
	}
	if got.GuardianPolicyConfig == nil || *got.GuardianPolicyConfig != policy {
		t.Fatalf("guardian policy config = %#v, want %q", got.GuardianPolicyConfig, policy)
	}
	if len(got.AllowedApprovalPolicies) != 1 || got.AllowedApprovalPolicies[0] != sandbox.ApprovalOnRequest {
		t.Fatalf("allowed approval policies = %#v, want [on-request]", got.AllowedApprovalPolicies)
	}
}
