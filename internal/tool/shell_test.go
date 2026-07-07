package tool

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/internal/sandbox"
)

func TestShellDeriveExecArgs(t *testing.T) {
	bash := &Shell{Type: ShellBash, Path: "/bin/bash"}
	if got := bash.DeriveExecArgs("echo hi", true); len(got) != 3 || got[1] != "-lc" {
		t.Fatalf("bash login args = %#v", got)
	}
	if got := bash.DeriveExecArgs("echo hi", false); len(got) != 3 || got[1] != "-c" {
		t.Fatalf("bash non-login args = %#v", got)
	}

	powershell := &Shell{Type: ShellPowerShell, Path: "pwsh"}
	got := powershell.DeriveExecArgs("Write-Output hi", false)
	if len(got) == 0 || got[len(got)-2] != "-Command" {
		t.Fatalf("powershell args = %#v", got)
	}
}

func TestResolveCommandRejectsLoginShellWhenDisabled(t *testing.T) {
	login := true
	_, err := ResolveCommand(&ExecCommandArgs{
		Cmd:   "echo hi",
		Login: &login,
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, false)
	if err == nil {
		t.Fatal("ResolveCommand returned nil error, want failure")
	}
}

func TestResolveCommandUsesRequestedShell(t *testing.T) {
	resolved, err := ResolveCommand(&ExecCommandArgs{
		Cmd:   "echo hi",
		Shell: "/bin/zsh",
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, true)
	if err != nil {
		t.Fatalf("ResolveCommand returned error: %v", err)
	}
	if resolved.ShellType != ShellZsh {
		t.Fatalf("ShellType = %q", resolved.ShellType)
	}
	if resolved.Command[0] != "/bin/zsh" {
		t.Fatalf("Command = %#v", resolved.Command)
	}
}

func TestBuildShellRequestDefaults(t *testing.T) {
	cwd := t.TempDir()
	req, err := BuildShellRequest(&ExecCommandArgs{
		Cmd: "echo hi",
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, ShellValidationOptions{
		AdditionalPermissionsAllowed: true,
		ApprovalPolicy:               sandbox.ApprovalOnRequest,
		CWD:                          cwd,
		Env:                          map[string]string{"A": "B"},
		DefaultTimeoutMS:             1234,
	})
	if err != nil {
		t.Fatalf("BuildShellRequest returned error: %v", err)
	}
	if req.CWD != cleanAbsForTest(cwd) {
		t.Fatalf("CWD = %q", req.CWD)
	}
	if req.YieldTimeMS != DefaultExecYieldTimeMS {
		t.Fatalf("YieldTimeMS = %d", req.YieldTimeMS)
	}
	if req.TimeoutMS != 1234 {
		t.Fatalf("TimeoutMS = %d", req.TimeoutMS)
	}
	if req.SandboxPermissions != sandbox.SandboxPermissionsUseDefault {
		t.Fatalf("SandboxPermissions = %q", req.SandboxPermissions)
	}
	if req.Env["A"] != "B" {
		t.Fatalf("Env = %#v", req.Env)
	}
}

func TestBuildShellRequestUsesCWDEnvAndTimeoutOverrides(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	req, err := BuildShellRequest(&ExecCommandArgs{
		Cmd:       "echo hi",
		CWD:       "sub",
		Env:       map[string]string{"A": "override", "B": "2"},
		TimeoutMS: 42,
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, ShellValidationOptions{
		AdditionalPermissionsAllowed: true,
		ApprovalPolicy:               sandbox.ApprovalOnRequest,
		CWD:                          base,
		Env:                          map[string]string{"A": "base"},
		DefaultTimeoutMS:             1234,
	})
	if err != nil {
		t.Fatalf("BuildShellRequest returned error: %v", err)
	}
	if req.CWD != cleanAbsForTest(sub) {
		t.Fatalf("CWD = %q", req.CWD)
	}
	if req.TimeoutMS != 42 {
		t.Fatalf("TimeoutMS = %d", req.TimeoutMS)
	}
	if req.Env["A"] != "override" || req.Env["B"] != "2" {
		t.Fatalf("Env = %#v", req.Env)
	}
}

func TestBuildShellRequestUsesWorkdirAlias(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	req, err := BuildShellRequest(&ExecCommandArgs{
		Cmd:     "echo hi",
		Workdir: sub,
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, ShellValidationOptions{
		AdditionalPermissionsAllowed: true,
		ApprovalPolicy:               sandbox.ApprovalOnRequest,
		CWD:                          base,
	})
	if err != nil {
		t.Fatalf("BuildShellRequest returned error: %v", err)
	}
	if req.CWD != cleanAbsForTest(sub) {
		t.Fatalf("CWD = %q", req.CWD)
	}
}

func TestBuildShellRequestRejectsEscalationWhenApprovalIsNever(t *testing.T) {
	_, err := BuildShellRequest(&ExecCommandArgs{
		Cmd:                "echo hi",
		SandboxPermissions: sandbox.SandboxPermissionsRequireEscalated,
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, ShellValidationOptions{
		AdditionalPermissionsAllowed: true,
		ApprovalPolicy:               sandbox.ApprovalNever,
		CWD:                          t.TempDir(),
	})
	if err == nil {
		t.Fatal("BuildShellRequest returned nil error, want failure")
	}
}

func TestBuildShellRequestMarksApprovalRequiredForSandboxOverride(t *testing.T) {
	req, err := BuildShellRequest(&ExecCommandArgs{
		Cmd:                "echo hi",
		SandboxPermissions: sandbox.SandboxPermissionsRequireEscalated,
		Justification:      "needs network",
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, ShellValidationOptions{
		AdditionalPermissionsAllowed: true,
		ApprovalPolicy:               sandbox.ApprovalOnRequest,
		CWD:                          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildShellRequest returned error: %v", err)
	}
	if !req.ApprovalRequired || req.ApprovalReason != "needs network" {
		t.Fatalf("approval = %v reason=%q", req.ApprovalRequired, req.ApprovalReason)
	}
}

func TestBuildShellRequestNormalizesAdditionalPermissions(t *testing.T) {
	cwd := t.TempDir()
	network := true
	req, err := BuildShellRequest(&ExecCommandArgs{
		Cmd:                "echo hi",
		SandboxPermissions: sandbox.SandboxPermissionsWithAdditionalPermissions,
		AdditionalPermissions: &sandbox.AdditionalPermissionProfile{
			Network:    &network,
			FileSystem: []string{"src"},
		},
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, ShellValidationOptions{
		AdditionalPermissionsAllowed: true,
		ApprovalPolicy:               sandbox.ApprovalOnRequest,
		CWD:                          cwd,
	})
	if err != nil {
		t.Fatalf("BuildShellRequest returned error: %v", err)
	}
	if req.AdditionalPermissions == nil || req.AdditionalPermissions.Network == nil || !*req.AdditionalPermissions.Network {
		t.Fatalf("AdditionalPermissions = %#v", req.AdditionalPermissions)
	}
	want := cleanAbsForTest(filepath.Join(cwd, "src"))
	if len(req.AdditionalPermissions.FileSystem) != 1 || req.AdditionalPermissions.FileSystem[0] != want {
		t.Fatalf("FileSystem = %#v", req.AdditionalPermissions.FileSystem)
	}
	if req.SandboxProfile == nil || !req.SandboxProfile.NetworkEnabled {
		t.Fatalf("SandboxProfile = %#v", req.SandboxProfile)
	}
	if len(req.SandboxProfile.WritableRoots) == 0 {
		t.Fatalf("SandboxProfile writable roots empty")
	}
}

func TestBuildShellRequestBuildsSandboxProfileFromPermissionProfile(t *testing.T) {
	cwd := t.TempDir()
	profile := sandbox.ReadOnlyPermissionProfile()
	req, err := BuildShellRequest(&ExecCommandArgs{
		Cmd: "echo hi",
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, ShellValidationOptions{
		ApprovalPolicy:    sandbox.ApprovalOnRequest,
		CWD:               cwd,
		PermissionProfile: &profile,
	})
	if err != nil {
		t.Fatalf("BuildShellRequest returned error: %v", err)
	}
	if req.SandboxProfile == nil || req.SandboxProfile.PolicyTag != "read-only" {
		t.Fatalf("SandboxProfile = %#v", req.SandboxProfile)
	}
	if req.SandboxProfile.NetworkEnabled {
		t.Fatalf("NetworkEnabled = true")
	}
}

func TestBuildShellRequestRequireEscalatedPreapprovalUsesFullAccessProfile(t *testing.T) {
	profile := sandbox.WorkspaceWritePermissionProfile()
	req, err := BuildShellRequest(&ExecCommandArgs{
		Cmd:                "go test ./...",
		SandboxPermissions: sandbox.SandboxPermissionsRequireEscalated,
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, ShellValidationOptions{
		ApprovalPolicy:         sandbox.ApprovalOnRequest,
		CWD:                    t.TempDir(),
		PermissionProfileID:    sandbox.BuiltInPermissionProfileWorkspace,
		PermissionProfile:      &profile,
		PermissionsPreapproved: true,
	})
	if err != nil {
		t.Fatalf("BuildShellRequest returned error: %v", err)
	}
	if req.PermissionProfile == nil || !req.PermissionProfile.Disabled || req.PermissionProfileID != sandbox.BuiltInPermissionProfileDangerFullAccess {
		t.Fatalf("PermissionProfile = %#v id %q, want full access", req.PermissionProfile, req.PermissionProfileID)
	}
}

func TestBuildShellRequestMergesAdditionalPermissionsIntoProfile(t *testing.T) {
	network := true
	extra := filepath.Join(t.TempDir(), "cache")
	profile := sandbox.ReadOnlyPermissionProfile()
	req, err := BuildShellRequest(&ExecCommandArgs{
		Cmd:                "go test ./...",
		SandboxPermissions: sandbox.SandboxPermissionsWithAdditionalPermissions,
		AdditionalPermissions: &sandbox.AdditionalPermissionProfile{
			Network:    &network,
			FileSystem: []string{extra},
		},
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, ShellValidationOptions{
		AdditionalPermissionsAllowed: true,
		ApprovalPolicy:               sandbox.ApprovalOnRequest,
		CWD:                          t.TempDir(),
		PermissionProfileID:          sandbox.BuiltInPermissionProfileReadOnly,
		PermissionProfile:            &profile,
		PermissionsPreapproved:       true,
	})
	if err != nil {
		t.Fatalf("BuildShellRequest returned error: %v", err)
	}
	if req.PermissionProfile == nil || !req.PermissionProfile.AllowsNetwork() {
		t.Fatalf("PermissionProfile network = %#v", req.PermissionProfile)
	}
	policy := req.PermissionProfile.LegacySandboxPolicy()
	if policy.Kind != sandbox.SandboxWorkspaceWrite || len(policy.WritableRoots) != 1 || policy.WritableRoots[0] != filepath.Clean(extra) {
		t.Fatalf("policy = %#v, want workspace write with extra root", policy)
	}
}

func TestRequestsSandboxOverride(t *testing.T) {
	if !RequestsSandboxOverride(sandbox.SandboxPermissionsRequireEscalated) {
		t.Fatal("RequireEscalated should request sandbox override")
	}
	if !RequestsSandboxOverride(sandbox.SandboxPermissionsWithAdditionalPermissions) {
		t.Fatal("WithAdditionalPermissions should request sandbox override")
	}
	if RequestsSandboxOverride(sandbox.SandboxPermissionsUseDefault) {
		t.Fatal("UseDefault should not request sandbox override")
	}
}

func cleanAbsForTest(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}
