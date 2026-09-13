package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
)

// TestRequirementsApprovalPolicyFallsBackLikeRust mirrors Rust's
// root_approval_policy_falls_back_when_disallowed_by_requirements: an explicit
// disallowed value falls back to the first allowed requirement with a warning,
// while the implicit default is corrected silently.
func TestRequirementsApprovalPolicyFallsBackLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`allowed_approval_policies = ["on-request"]`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	values := map[string]any{"approval_policy": "never"}
	if err := applyManagedConstrainedOverrides(values, requirements); err != nil {
		t.Fatalf("applyManagedConstrainedOverrides() error = %v", err)
	}
	if values["approval_policy"] != string(sandbox.ApprovalOnRequest) {
		t.Fatalf("approval_policy = %#v, want on-request", values["approval_policy"])
	}
	warnings := StartupWarnings(map[string]any{"approval_policy": "never"}, requirements)
	want := "Configured value for `approval_policy` is disallowed by requirements; falling back to required value OnRequest. Details: invalid value for `approval_policy`: `Never` is not in the allowed set [OnRequest] (set by managed requirements)"
	if len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("StartupWarnings() = %#v, want [%q]", warnings, want)
	}
	// The implicit default is constrained without a warning (Rust's
	// approval_policy_was_explicit guard).
	implicit := map[string]any{}
	if err := applyManagedConstrainedOverrides(implicit, requirements); err != nil {
		t.Fatalf("applyManagedConstrainedOverrides() error = %v", err)
	}
	if implicit["approval_policy"] != string(sandbox.ApprovalOnRequest) {
		t.Fatalf("implicit approval_policy = %#v, want on-request", implicit["approval_policy"])
	}
	if warnings := StartupWarnings(implicit, requirements); len(warnings) != 0 {
		t.Fatalf("StartupWarnings() = %#v, want none for the implicit default", warnings)
	}
}

// TestRequirementsApprovalsReviewerFallsBackLikeRust mirrors Rust's
// root_approvals_reviewer_falls_back_when_disallowed_by_requirements, including
// the legacy guardian_subagent spelling of the managed reviewer.
func TestRequirementsApprovalsReviewerFallsBackLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`allowed_approvals_reviewers = ["guardian_subagent"]`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	values := map[string]any{"approvals_reviewer": "user"}
	if err := applyManagedConstrainedOverrides(values, requirements); err != nil {
		t.Fatalf("applyManagedConstrainedOverrides() error = %v", err)
	}
	if values["approvals_reviewer"] != string(ApprovalsReviewerAutoReview) {
		t.Fatalf("approvals_reviewer = %#v, want auto_review", values["approvals_reviewer"])
	}
	warnings := StartupWarnings(map[string]any{"approvals_reviewer": "user"}, requirements)
	want := "Configured value for `approvals_reviewer` is disallowed by requirements; falling back to required value AutoReview. Details: invalid value for `approvals_reviewer`: `User` is not in the allowed set [AutoReview] (set by managed requirements)"
	if len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("StartupWarnings() = %#v, want [%q]", warnings, want)
	}
}

// TestRequirementsWebSearchModeFallsBackLikeRust mirrors Rust's
// test_requirements_web_search_mode_allowlist_does_not_warn_when_unset and
// requirements_web_search_mode_overrides_danger_full_access_default.
func TestRequirementsWebSearchModeFallsBackLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`allowed_web_search_modes = ["cached"]`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	// The implicit cached default is allowed, so nothing changes and nothing is
	// reported.
	implicit := map[string]any{}
	if err := applyManagedConstrainedOverrides(implicit, requirements); err != nil {
		t.Fatalf("applyManagedConstrainedOverrides() error = %v", err)
	}
	if implicit["web_search"] != "cached" {
		t.Fatalf("implicit web_search = %#v, want cached", implicit["web_search"])
	}
	if warnings := StartupWarnings(implicit, requirements); len(warnings) != 0 {
		t.Fatalf("StartupWarnings() = %#v, want none for the allowed default", warnings)
	}
	// An explicit live mode falls back to the required mode with a warning.
	values := map[string]any{"web_search": "live"}
	if err := applyManagedConstrainedOverrides(values, requirements); err != nil {
		t.Fatalf("applyManagedConstrainedOverrides() error = %v", err)
	}
	if values["web_search"] != "cached" {
		t.Fatalf("web_search = %#v, want cached", values["web_search"])
	}
	warnings := StartupWarnings(map[string]any{"web_search": "live"}, requirements)
	want := "Configured value for `web_search_mode` is disallowed by requirements; falling back to required value Cached. Details: invalid value for `web_search_mode`: `Live` is not in the allowed set [Disabled, Cached] (set by managed requirements)"
	if len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("StartupWarnings() = %#v, want [%q]", warnings, want)
	}
}

// TestManagedConstrainedOverridesThroughRequirementsFile pins the load path: a
// requirements.toml allow-list constrains the effective config values.
func TestManagedConstrainedOverridesThroughRequirementsFile(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte(strings.Join([]string{
		`approval_policy = "never"`,
		`approvals_reviewer = "user"`,
		`web_search = "live"`,
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(strings.Join([]string{
		`allowed_approval_policies = ["on-request"]`,
		`allowed_approvals_reviewers = ["auto_review"]`,
		`allowed_web_search_modes = ["cached"]`,
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEffective(home, nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadEffective() error = %v", err)
	}
	if cfg.Values["approval_policy"] != string(sandbox.ApprovalOnRequest) {
		t.Fatalf("approval_policy = %#v, want on-request", cfg.Values["approval_policy"])
	}
	if cfg.Values["approvals_reviewer"] != string(ApprovalsReviewerAutoReview) {
		t.Fatalf("approvals_reviewer = %#v, want auto_review", cfg.Values["approvals_reviewer"])
	}
	if cfg.Values["web_search"] != "cached" {
		t.Fatalf("web_search = %#v, want cached", cfg.Values["web_search"])
	}
}
