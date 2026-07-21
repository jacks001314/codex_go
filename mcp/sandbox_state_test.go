package mcp

import "testing"

func TestSandboxStateEnvNil(t *testing.T) {
	env := BuildSandboxStateEnv(nil)
	if len(env) != 0 {
		t.Fatalf("nil sandbox state should produce no env: got %#v", env)
	}
}

func TestSandboxStateEnvBuildsCorrectEnv(t *testing.T) {
	legacyLandlock := true
	state := &SandboxState{
		PermissionProfile:    "workspace_write",
		CodexLinuxSandboxExe: "/usr/bin/codex-sandbox",
		SandboxCWD:           "/home/user/project",
		UseLegacyLandlock:    &legacyLandlock,
	}
	env := BuildSandboxStateEnv(state)
	if env["CODEX_PERMISSION_PROFILE"] != "workspace_write" {
		t.Fatalf("permission profile = %q", env["CODEX_PERMISSION_PROFILE"])
	}
	if env["CODEX_LINUX_SANDBOX_EXE"] != "/usr/bin/codex-sandbox" {
		t.Fatalf("linux sandbox exe = %q", env["CODEX_LINUX_SANDBOX_EXE"])
	}
	if env["CODEX_SANDBOX_CWD"] != "/home/user/project" {
		t.Fatalf("sandbox cwd = %q", env["CODEX_SANDBOX_CWD"])
	}
	if env["CODEX_USE_LEGACY_LANDLOCK"] != "1" {
		t.Fatalf("use legacy landlock = %q", env["CODEX_USE_LEGACY_LANDLOCK"])
	}
}

func TestSandboxStateEnvOmitEmpty(t *testing.T) {
	state := &SandboxState{
		PermissionProfile: "read_only",
	}
	env := BuildSandboxStateEnv(state)
	if len(env) != 1 {
		t.Fatalf("expected only permission_profile for minimal state: got %#v", env)
	}
}

func TestMergeSandboxEnvPreservesBaseValues(t *testing.T) {
	base := map[string]string{"PATH": "/usr/bin", "HOME": "/home/user"}
	state := &SandboxState{
		PermissionProfile: "workspace_write",
		SandboxCWD:        "/home/user/project",
	}
	merged := MergeSandboxEnv(base, state)
	if merged["PATH"] != "/usr/bin" || merged["HOME"] != "/home/user" {
		t.Fatalf("base env lost: %#v", merged)
	}
	if merged["CODEX_PERMISSION_PROFILE"] != "workspace_write" {
		t.Fatalf("sandbox env missing: %#v", merged)
	}
}

func TestMergeSandboxEnvDoesNotOverrideExisting(t *testing.T) {
	base := map[string]string{"CODEX_PERMISSION_PROFILE": "existing_profile"}
	state := &SandboxState{PermissionProfile: "new_profile"}
	merged := MergeSandboxEnv(base, state)
	if merged["CODEX_PERMISSION_PROFILE"] != "existing_profile" {
		t.Fatalf("sandbox env should not override existing: got %q", merged["CODEX_PERMISSION_PROFILE"])
	}
}
