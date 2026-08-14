package appserver

import (
	"testing"

	"codex_go/config"
)

func TestThreadStartEffectivePermissionsTrustProjectUsesEffectiveProfile(t *testing.T) {
	cwd := t.TempDir()
	readOnly := "read-only"
	workspace := "workspace"

	readOnlyCfg := &config.Config{Requirements: &config.ConfigRequirements{DefaultPermissions: &readOnly}}
	if threadStartEffectivePermissionsTrustProject(readOnlyCfg, cwd, &ThreadStartParams{CWD: cwd}) {
		t.Fatal("read-only effective profile should not trust the project")
	}

	workspaceCfg := &config.Config{Requirements: &config.ConfigRequirements{DefaultPermissions: &workspace}}
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
