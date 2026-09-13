package appserver

import (
	"testing"

	"codex_go/config"
)

func TestThreadStartEffectivePermissionsTrustProjectUsesEffectiveProfile(t *testing.T) {
	cwd := t.TempDir()
	readOnly := "read-only"
	workspace := "workspace"

	// Managed requirements name their required default through the allow-list
	// (Rust validate_required_permission_profile_catalog), so the fixtures use
	// the managed shape rather than a bare default_permissions override.
	readOnlyCfg := &config.Config{Requirements: &config.ConfigRequirements{
		DefaultPermissions:        &readOnly,
		AllowedPermissionProfiles: map[string]bool{"read-only": true},
	}}
	if threadStartEffectivePermissionsTrustProject(readOnlyCfg, cwd, &ThreadStartParams{CWD: cwd}) {
		t.Fatal("read-only effective profile should not trust the project")
	}

	workspaceCfg := &config.Config{Requirements: &config.ConfigRequirements{
		DefaultPermissions:        &workspace,
		AllowedPermissionProfiles: map[string]bool{"workspace": true},
	}}
	if !threadStartEffectivePermissionsTrustProject(workspaceCfg, cwd, &ThreadStartParams{CWD: cwd}) {
		t.Fatal("workspace effective profile should trust the project")
	}

	if !threadStartEffectivePermissionsTrustProject(&config.Config{}, cwd, &ThreadStartParams{CWD: cwd, Sandbox: "workspace-write"}) {
		t.Fatal("requested workspace-write should keep trusting when no managed downgrade is configured")
	}
}

func TestThreadStartEffectivePermissionsTrustProjectPermissionsPath(t *testing.T) {
	cwd := t.TempDir()
	cfg := &config.Config{}
	readOnly := "read-only"
	workspace := "workspace"
	if threadStartEffectivePermissionsTrustProject(cfg, cwd, &ThreadStartParams{CWD: cwd, Permissions: &readOnly}) {
		t.Fatal("read-only permission profile should not trust the project")
	}
	if !threadStartEffectivePermissionsTrustProject(cfg, cwd, &ThreadStartParams{CWD: cwd, Permissions: &workspace}) {
		t.Fatal("workspace permission profile should trust the project")
	}
}
