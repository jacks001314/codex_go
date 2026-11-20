package config

import (
	"os"
	"path/filepath"
	"testing"
)

func guardianRefreshBundle() *CloudConfigBundle {
	return &CloudConfigBundle{
		ConfigTOML: CloudConfigTOMLBundle{EnterpriseManaged: []CloudConfigFragment{
			{ID: "workspace-top", Name: "Workspace top", Contents: "model = 'irrelevant'"},
			{ID: "workspace-scalar", Name: "Workspace scalar", Contents: "auto_review = 1"},
			{ID: "workspace-low", Name: "Workspace low", Contents: "auto_review.policy = 'lower priority'"},
		}},
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{
			{ID: "reviewers", Name: "reviewers", Contents: "allowed_approvals_reviewers = ['auto_review']"},
		}},
	}
}

// guardianRefreshLoader publishes the bundle through an enterprise policy so the
// snapshot carries the policy revision binding it was resolved from, like the
// Rust fixture's into_loader.
func guardianRefreshLoader(bundle *CloudConfigBundle) *CloudConfigLoader {
	loader := NewCloudConfigLoader(func() (*CloudConfigBundle, error) { return bundle, nil })
	policy := NewCloudConfigBundlePolicy()
	policy.ObserveRemoteBundle(bundle)
	published := CloudConfigBundleSnapshot{Bundle: bundle}
	policy.PublishSnapshot(&published)
	return loader.WithEMAPolicySnapshots(policy, func() CloudConfigBundleSnapshot { return published })
}

func guardianRefreshStack(sessionAutoReview any) *Config {
	stack := &Config{
		Values: map[string]any{},
		featureLayers: []Layer{
			{
				Name:   LayerSource{Type: LayerSourceEnterpriseManaged, ID: "workspace", Name: "Workspace"},
				Config: map[string]any{"auto_review": map[string]any{"policy": "old policy"}},
			},
			{Name: LayerSource{Type: LayerSourceSessionFlags}, Config: map[string]any{"model": "session-model"}},
		},
	}
	if sessionAutoReview != nil {
		stack.featureLayers[len(stack.featureLayers)-1].Config = map[string]any{
			"model":       "session-model",
			"auto_review": sessionAutoReview,
		}
	}
	return stack
}

// Mirrors Rust #62274: a Guardian refresh publishes reviewer restrictions,
// required models and policy text without rebuilding the configuration, keeps
// unrelated requirements, validates the merged fallbacks after a session
// override, and drops the old policy text once its bundle disappears.
func TestGuardianRefreshValidatesFallbackAfterMergingRetainedLayersLikeRust(t *testing.T) {
	home := t.TempDir()
	managedPath := filepath.Join(home, "managed.toml")
	if err := os.WriteFile(managedPath, []byte("sandbox_mode = 'invalid'\n"), 0o600); err != nil {
		t.Fatalf("write managed config: %v", err)
	}
	t.Setenv(appServerManagedConfigPathEnv, managedPath)

	allowLoginShell := false
	stack := guardianRefreshStack(nil)
	stack.Requirements = &ConfigRequirements{AllowLoginShell: &allowLoginShell}

	snapshot, err := LoadGuardianPolicySnapshot(home, guardianRefreshLoader(guardianRefreshBundle()), 1)
	if err != nil {
		t.Fatalf("LoadGuardianPolicySnapshot() error = %v", err)
	}
	if snapshot.Revision != 1 {
		t.Fatalf("revision = %d, want 1", snapshot.Revision)
	}
	if snapshot.Requirements == nil {
		t.Fatal("snapshot carried no requirements")
	}
	if len(snapshot.ManagedConfig) != 2 {
		t.Fatalf("managed fallbacks = %#v, want the two auto_review fragments", snapshot.ManagedConfig)
	}
	low, ok := snapshot.ManagedConfig[0].Value.(map[string]any)
	if !ok || low["policy"] != "lower priority" {
		t.Fatalf("lowest fallback = %#v, want the lowest-priority policy first", snapshot.ManagedConfig[0].Value)
	}
	reviewer, _, err := resolveRequirementConstrainedApprovalsReviewer("user", snapshot.Requirements.AllowedApprovalsReviewers)
	if err != nil {
		t.Fatalf("resolve reviewer error = %v", err)
	}
	if reviewer == string(ApprovalsReviewerUser) {
		t.Fatal("the published reviewer restriction still allowed the user reviewer")
	}

	retained := guardianRefreshStack(nil)
	retained.Requirements = &ConfigRequirements{AllowLoginShell: &allowLoginShell}
	if err := stack.ApplyGuardianPolicy(snapshot); err == nil {
		t.Fatal("an invalid merged auto_review was accepted")
	}
	retained.RetainGuardianPolicyFrom(stack)
	if stack.Requirements.AllowLoginShell == nil || *stack.Requirements.AllowLoginShell {
		t.Fatal("the refresh rewrote an unrelated requirement")
	}
	if stack.GuardianPolicyRevision() != 1 {
		t.Fatalf("revision = %d, want 1", stack.GuardianPolicyRevision())
	}
	if stack.GuardianPolicyBinding() == nil {
		t.Fatal("the refresh published no source binding")
	}
	if _, ok := stack.GuardianPolicyConfig(); ok {
		t.Fatal("the invalid managed fallback still resolved a policy")
	}
	if retained.GuardianPolicyRevision() != stack.GuardianPolicyRevision() {
		t.Fatalf("retained revision = %d, want %d", retained.GuardianPolicyRevision(), stack.GuardianPolicyRevision())
	}

	// A session override outranks the invalid managed scalar, so the merged
	// policy becomes valid again.
	overridden := *stack
	overridden.featureLayers = append([]Layer{}, stack.featureLayers...)
	overridden.featureLayers[len(overridden.featureLayers)-1] = Layer{
		Name:   LayerSource{Type: LayerSourceSessionFlags},
		Config: map[string]any{"model": "session-model", "auto_review": map[string]any{"policy": "local"}},
	}
	policy, ok := overridden.GuardianPolicyConfig()
	if !ok || policy != "local" {
		t.Fatalf("policy = %q (set = %t), want the session override", policy, ok)
	}

	// Removing the bundle must not revive the managed fallback it published.
	removed, err := LoadGuardianPolicySnapshot(home, guardianRefreshLoader(nil), snapshot.Revision+1)
	if err != nil {
		t.Fatalf("LoadGuardianPolicySnapshot() after removal error = %v", err)
	}
	if err := stack.ApplyGuardianPolicy(removed); err != nil {
		t.Fatalf("ApplyGuardianPolicy() after removal error = %v", err)
	}
	if policy, ok := stack.GuardianPolicyConfig(); ok {
		t.Fatalf("stale policy %q survived the bundle removal", policy)
	}
	if stack.GuardianPolicyRevision() != 2 {
		t.Fatalf("revision = %d, want 2", stack.GuardianPolicyRevision())
	}
}
