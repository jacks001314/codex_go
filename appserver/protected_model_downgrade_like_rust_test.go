package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
)

// TestProtectedModelStartupDowngradesViaProfileLikeRust pins Rust #59933: the
// startup restriction for a model that requires automatic review is decided by
// the effective permission profile, and the settings entry point judges the
// same profile through the same predicate.
func TestProtectedModelStartupDowngradesViaProfileLikeRust(t *testing.T) {
	managed := func(t *testing.T, raw string) *config.SandboxPermissionProfileResolution {
		t.Helper()
		profile, err := sandbox.ParseRuntimePermissionProfileJSON(raw)
		if err != nil {
			t.Fatalf("ParseRuntimePermissionProfileJSON(%s) error = %v", raw, err)
		}
		return &config.SandboxPermissionProfileResolution{ID: "managed", Profile: profile}
	}

	// `Managed{Unrestricted, Enabled}` is full disk write with network but is not
	// the legacy danger-full-access shape, which the old check missed.
	unrestricted := managed(t, `{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"}`)
	downgraded := protectedModelStartupPermissionProfile(true, unrestricted)
	if downgraded == unrestricted {
		t.Fatal("a managed unrestricted profile must be downgraded at startup")
	}
	if downgraded.ID != sandbox.BuiltInPermissionProfileWorkspace || downgraded.Profile == nil ||
		downgraded.Profile.SandboxPolicy == nil ||
		downgraded.Profile.SandboxPolicy.Kind != sandbox.SandboxWorkspaceWrite ||
		downgraded.Profile.SandboxPolicy.HasFullDiskWriteAccess() {
		t.Fatalf("downgraded profile = %#v, want workspace-write", downgraded.Profile)
	}
	if !sandbox.PermissionProfileHasFullDiskWriteAccess(unrestricted.Profile) {
		t.Fatal("the settings entry point must see the same full-disk profile")
	}

	// The legacy full-access shape keeps its behaviour.
	fullAccess := &config.SandboxPermissionProfileResolution{
		ID:      sandbox.BuiltInPermissionProfileDangerFullAccess,
		Profile: ptrPermissionProfile(sandbox.FullAccessPermissionProfile()),
	}
	if got := protectedModelStartupPermissionProfile(true, fullAccess); got == fullAccess {
		t.Fatal("danger-full-access must still be downgraded")
	}

	// A nil profile declares no capability, so it is neither full access nor a
	// downgrade trigger (the old predicate answered the opposite way).
	if sandbox.PermissionProfileHasFullDiskWriteAccess(nil) {
		t.Fatal("a nil profile must not be classified as full-disk write access")
	}
	if got := protectedModelStartupPermissionProfile(true, &config.SandboxPermissionProfileResolution{ID: "empty"}); got.Profile != nil {
		t.Fatalf("a profile-less resolution was downgraded: %#v", got.Profile)
	}

	// Restricted network keeps its permissions.
	restricted := managed(t, `{"type":"managed","file_system":{"type":"unrestricted"},"network":"disabled"}`)
	if got := protectedModelStartupPermissionProfile(true, restricted); got != restricted {
		t.Fatal("a network-restricted profile must keep its permissions")
	}

	// External enforcement owns the boundary.
	external := &config.SandboxPermissionProfileResolution{
		ID: "external",
		Profile: &sandbox.PermissionProfile{
			SandboxPolicy: sandbox.NewExternalSandboxPolicy(sandbox.NetworkEnabled),
		},
	}
	if got := protectedModelStartupPermissionProfile(true, external); got != external {
		t.Fatal("external enforcement must be preserved")
	}

	// An unprotected model never downgrades.
	if got := protectedModelStartupPermissionProfile(false, unrestricted); got != unrestricted {
		t.Fatal("an unprotected model must keep its permissions")
	}
}

func ptrPermissionProfile(profile sandbox.PermissionProfile) *sandbox.PermissionProfile {
	return &profile
}
