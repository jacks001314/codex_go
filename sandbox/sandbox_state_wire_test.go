package sandbox

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// Rust's authoritative wire spelling for the sandbox state is camelCase
// (codex_mcp::SandboxState, consumed by codex-rs/cli/src/debug_sandbox.rs).
// These tests pin the camelCase primary path and the retained snake_case
// compatibility alias of this repository.

func TestParseSandboxStateJSONAcceptsCamelCaseLikeRust(t *testing.T) {
	cwd := t.TempDir()
	raw := fmt.Sprintf(
		`{"permissionProfile":{"type":"disabled"},"codexLinuxSandboxExe":"/usr/bin/codex-linux-sandbox","sandboxCwd":%q,"useLegacyLandlock":true,"useMxc":true,"futureField":{"preserved":true}}`,
		cwd,
	)
	state, err := parseSandboxStateJSON(raw)
	if err != nil {
		t.Fatalf("parseSandboxStateJSON() error = %v", err)
	}
	if state.PermissionProfile == nil || !state.PermissionProfile.Disabled {
		t.Fatalf("permission profile = %#v, want disabled", state.PermissionProfile)
	}
	if state.SandboxCWD != cwd {
		t.Fatalf("sandbox cwd = %q, want %q", state.SandboxCWD, cwd)
	}
	if state.CodexLinuxSandboxExe != "/usr/bin/codex-linux-sandbox" {
		t.Fatalf("linux sandbox exe = %q", state.CodexLinuxSandboxExe)
	}
	if !state.HasUseLegacyLandlock || !state.UseLegacyLandlock {
		t.Fatalf("useLegacyLandlock = %t (present %t), want true", state.UseLegacyLandlock, state.HasUseLegacyLandlock)
	}
	if !state.HasUseMXC || !state.UseMXC {
		t.Fatalf("useMxc = %t (present %t), want true", state.UseMXC, state.HasUseMXC)
	}
}

func TestParseSandboxStateJSONKeepsSnakeCaseAlias(t *testing.T) {
	cwd := t.TempDir()
	raw := fmt.Sprintf(
		`{"permission_profile":{"type":"disabled"},"codex_linux_sandbox_exe":"/usr/bin/codex-linux-sandbox","sandbox_cwd":%q,"use_legacy_landlock":true,"use_mxc":true}`,
		cwd,
	)
	state, err := parseSandboxStateJSON(raw)
	if err != nil {
		t.Fatalf("parseSandboxStateJSON() error = %v", err)
	}
	if state.PermissionProfile == nil || !state.PermissionProfile.Disabled {
		t.Fatalf("permission profile = %#v, want disabled", state.PermissionProfile)
	}
	if state.SandboxCWD != cwd {
		t.Fatalf("sandbox cwd = %q, want %q", state.SandboxCWD, cwd)
	}
	if state.CodexLinuxSandboxExe != "/usr/bin/codex-linux-sandbox" {
		t.Fatalf("linux sandbox exe = %q", state.CodexLinuxSandboxExe)
	}
	if !state.HasUseLegacyLandlock || !state.UseLegacyLandlock {
		t.Fatalf("use_legacy_landlock = %t (present %t), want true", state.UseLegacyLandlock, state.HasUseLegacyLandlock)
	}
	if !state.HasUseMXC || !state.UseMXC {
		t.Fatalf("use_mxc = %t (present %t), want true", state.UseMXC, state.HasUseMXC)
	}
}

func TestParseSandboxStateJSONPrefersCamelCaseWhenBothSpellingsPresent(t *testing.T) {
	camelCWD := t.TempDir()
	legacyCWD := t.TempDir()
	raw := fmt.Sprintf(
		`{"permissionProfile":{"type":"disabled"},"permission_profile":{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"},"sandboxCwd":%q,"sandbox_cwd":%q}`,
		camelCWD, legacyCWD,
	)
	state, err := parseSandboxStateJSON(raw)
	if err != nil {
		t.Fatalf("parseSandboxStateJSON() error = %v", err)
	}
	if state.PermissionProfile == nil || !state.PermissionProfile.Disabled {
		t.Fatalf("camelCase permissionProfile must win: %#v", state.PermissionProfile)
	}
	if state.SandboxCWD != camelCWD {
		t.Fatalf("sandbox cwd = %q, want the camelCase %q", state.SandboxCWD, camelCWD)
	}
}

func TestParseSandboxStateJSONStillReportsMissingPermissionProfile(t *testing.T) {
	raw := fmt.Sprintf(`{"sandboxCwd":%q}`, t.TempDir())
	_, err := parseSandboxStateJSON(raw)
	if err == nil || !strings.Contains(err.Error(), "missing permission_profile") {
		t.Fatalf("parseSandboxStateJSON() error = %v, want the missing permission_profile error", err)
	}
}

// TestBuildCommandRunPlanSelectsWindowsMXCFromForwardedState mirrors Rust
// cli/src/debug_sandbox.rs (#52182): a forwarded useMxc selects the native MXC
// backend, and an absent value keeps the restricted-token default.
func TestBuildCommandRunPlanSelectsWindowsMXCFromForwardedState(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the MXC backend selection is Windows-only; run this on the Windows node")
	}
	profile := WorkspaceWritePermissionProfile()
	profileJSON, err := RuntimePermissionProfileJSON(profile)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	cwd := t.TempDir()
	cases := []struct {
		name string
		raw  string
		want SandboxType
	}{
		{
			name: "camelCase useMxc",
			raw:  fmt.Sprintf(`{"permissionProfile":%s,"sandboxCwd":%q,"useMxc":true}`, profileJSON, cwd),
			want: SandboxTypeWindowsMxc,
		},
		{
			name: "snake_case use_mxc alias",
			raw:  fmt.Sprintf(`{"permission_profile":%s,"sandbox_cwd":%q,"use_mxc":true}`, profileJSON, cwd),
			want: SandboxTypeWindowsMxc,
		},
		{
			name: "absent useMxc keeps the restricted token",
			raw:  fmt.Sprintf(`{"permissionProfile":%s,"sandboxCwd":%q}`, profileJSON, cwd),
			want: SandboxTypeWindowsRestrictedToken,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := BuildCommandRunPlan(&CommandRunRequest{
				SandboxStateJSON: tc.raw,
				Command:          []string{"go", "version"},
			})
			if err != nil {
				t.Fatalf("BuildCommandRunPlan() error = %v", err)
			}
			if plan.SandboxType != tc.want {
				t.Fatalf("SandboxType = %q, want %q", plan.SandboxType, tc.want)
			}
		})
	}
}
