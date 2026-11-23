package config

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

// Rust #62276: an MCP configuration refresh reloads the managed requirement
// files, but it must keep a Guardian policy that was published while the
// refresh was running. Only the Guardian-owned requirements stay pinned; the
// rest of the file still applies.
func TestConfigServiceReloadKeepsPublishedGuardianPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	body := "allowed_approval_policies = [\"on-request\"]\n"
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	service := NewConfigService(home)
	policy := "Managed Guardian policy"
	service.PublishGuardianPolicyRequirements(&ConfigRequirements{
		AllowedApprovalsReviewers: []ApprovalsReviewer{ApprovalsReviewerUser},
		GuardianPolicyConfig:      &policy,
	})

	// The reload an MCP refresh performs re-reads requirements.toml.
	if err := service.ReloadRequirementsFromHome(); err != nil {
		t.Fatalf("reload requirements: %v", err)
	}

	got := service.Requirements().Requirements
	if got == nil {
		t.Fatal("requirements = nil after reload")
	}
	if len(got.AllowedApprovalsReviewers) != 1 || got.AllowedApprovalsReviewers[0] != ApprovalsReviewerUser {
		t.Fatalf("allowed approvals reviewers = %#v, want [user]", got.AllowedApprovalsReviewers)
	}
	if got.GuardianPolicyConfig == nil || *got.GuardianPolicyConfig != policy {
		t.Fatalf("guardian policy config = %#v, want %q", got.GuardianPolicyConfig, policy)
	}
	if len(got.AllowedApprovalPolicies) != 1 || got.AllowedApprovalPolicies[0] != sandbox.ApprovalOnRequest {
		t.Fatalf("allowed approval policies = %#v, want [on-request] from the reloaded file", got.AllowedApprovalPolicies)
	}
}
