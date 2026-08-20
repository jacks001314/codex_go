package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"codex_go/network"
	"codex_go/sandbox"
	shellutil "codex_go/shell"
)

func TestBuildShellRequestCarriesManagedNetworkContextLikeRust(t *testing.T) {
	managed := &network.ProxyManagedNetworkSandboxContext{LoopbackPorts: []uint16{43123, 48081}, AllowLocalBinding: true}
	req, err := BuildShellRequest(&ExecCommandArgs{Cmd: "echo ok"}, &Shell{Type: ShellBash, Path: "/bin/sh"}, ShellValidationOptions{
		CWD:                   t.TempDir(),
		EnforceManagedNetwork: true,
		ManagedNetwork:        managed,
	})
	if err != nil {
		t.Fatalf("BuildShellRequest() error = %v", err)
	}
	if !req.EnforceManagedNetwork || req.ManagedNetwork == nil || !reflect.DeepEqual(req.ManagedNetwork.LoopbackPorts, managed.LoopbackPorts) || !req.ManagedNetwork.AllowLocalBinding {
		t.Fatalf("managed network request = %#v", req.ManagedNetwork)
	}
	req.ManagedNetwork.LoopbackPorts[0] = 1
	if managed.LoopbackPorts[0] != 43123 {
		t.Fatal("BuildShellRequest() did not clone managed network ports")
	}
}

func TestShellDeriveExecArgs(t *testing.T) {
	bash := &Shell{Type: ShellBash, Path: "/bin/bash"}
	if got := bash.DeriveExecArgs("echo hi", true); len(got) != 3 || got[1] != "-lc" {
		t.Fatalf("bash login args = %#v", got)
	}
	if got := bash.DeriveExecArgs("echo hi", false); len(got) != 3 || got[1] != "-c" {
		t.Fatalf("bash non-login args = %#v", got)
	}

	powershell := &Shell{Type: ShellPowerShell, Path: "pwsh"}
	if got := powershell.DeriveExecArgs("Write-Output hi", false); !stringSlicesEqual(got, []string{"pwsh", "-NoProfile", "-Command", "Write-Output hi"}) {
		t.Fatalf("powershell non-login args = %#v", got)
	}
	if got := powershell.DeriveExecArgs("Write-Output hi", true); !stringSlicesEqual(got, []string{"pwsh", "-Command", "Write-Output hi"}) {
		t.Fatalf("powershell login args = %#v", got)
	}

	cmd := &Shell{Type: ShellCmd, Path: "cmd"}
	if got := cmd.DeriveExecArgs("echo hi", false); !stringSlicesEqual(got, []string{"cmd", "/c", "echo hi"}) {
		t.Fatalf("cmd args = %#v", got)
	}
}

func TestResolveCommandWithOptionsResolvesModelProvidedShellByType(t *testing.T) {
	binDir := t.TempDir()
	executableName := "bash"
	if runtime.GOOS == "windows" {
		executableName = "bash.exe"
	}
	executable := filepath.Join(binDir, executableName)
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", executable, err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHELL", "")

	attackerShell := filepath.Join(t.TempDir(), "attacker", "bash")
	resolved, err := ResolveCommandWithOptions(
		&ExecCommandArgs{Cmd: "echo hello", Shell: attackerShell},
		nil,
		CommandResolutionOptions{AllowLoginShell: true},
	)
	if err != nil {
		t.Fatalf("ResolveCommandWithOptions() error = %v", err)
	}
	if resolved.ShellType != ShellBash {
		t.Fatalf("shell type = %q, want bash", resolved.ShellType)
	}
	if len(resolved.Command) != 3 || resolved.Command[0] != executable || resolved.Command[1] != "-lc" || resolved.Command[2] != "echo hello" {
		t.Fatalf("resolved command = %#v, want [%s -lc echo hello]", resolved.Command, executable)
	}
	if strings.Contains(resolved.Command[0], "attacker") {
		t.Fatalf("model-provided path must not be used as the executable: %q", resolved.Command[0])
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
	binDir := t.TempDir()
	executableName := "zsh"
	if runtime.GOOS == "windows" {
		executableName = "zsh.exe"
	}
	executable := filepath.Join(binDir, executableName)
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", executable, err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHELL", "")

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
	if len(resolved.Command) != 3 || resolved.Command[0] != executable {
		t.Fatalf("Command = %#v", resolved.Command)
	}
}

func TestResolveCommandMatchesRustUnifiedExecExplicitShells(t *testing.T) {
	for _, tc := range []struct {
		name     string
		shell    string
		binNames []string
		wantType ShellType
		flags    []string
	}{
		{name: "bash", shell: "/bin/bash", binNames: []string{"bash"}, wantType: ShellBash, flags: []string{"-lc"}},
		{name: "powershell", shell: "powershell", binNames: []string{"pwsh"}, wantType: ShellPowerShell, flags: []string{"-Command"}},
		{name: "cmd", shell: "cmd", binNames: []string{"cmd"}, wantType: ShellCmd, flags: []string{"/c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			executableName := tc.binNames[0]
			if runtime.GOOS == "windows" {
				executableName += ".exe"
			}
			executable := filepath.Join(binDir, executableName)
			if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o700); err != nil {
				t.Fatalf("WriteFile(%s) error = %v", executable, err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SHELL", "")
			resolved, err := ResolveCommand(&ExecCommandArgs{
				Cmd:   "echo hello",
				Shell: tc.shell,
			}, &Shell{Type: ShellBash, Path: "/bin/bash"}, true)
			if err != nil {
				t.Fatalf("ResolveCommand returned error: %v", err)
			}
			wantArgv := append([]string{executable}, tc.flags...)
			wantArgv = append(wantArgv, "echo hello")
			if resolved.ShellType != tc.wantType || !stringSlicesEqual(resolved.Command, wantArgv) {
				t.Fatalf("resolved = %#v", resolved)
			}
		})
	}
}

func TestResolveCommandRejectsExplicitShellInZshForkModeLikeRust(t *testing.T) {
	_, err := ResolveCommandWithOptions(&ExecCommandArgs{
		Cmd:   "echo hello",
		Shell: "/bin/bash",
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, CommandResolutionOptions{
		AllowLoginShell: true,
		ShellMode:       UnifiedExecShellModeZshFork,
		ZshForkShell:    &Shell{Type: ShellZsh, Path: "/opt/codex/zsh"},
	})
	if err == nil || !strings.Contains(err.Error(), "`shell` is not supported for local zsh-fork exec") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveCommandUsesZshForkShellWhenConfiguredLikeRust(t *testing.T) {
	resolved, err := ResolveCommandWithOptions(&ExecCommandArgs{
		Cmd: "echo hello",
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, CommandResolutionOptions{
		AllowLoginShell: true,
		ShellMode:       UnifiedExecShellModeZshFork,
		ZshForkShell:    &Shell{Type: ShellZsh, Path: "/opt/codex/zsh"},
	})
	if err != nil {
		t.Fatalf("ResolveCommandWithOptions returned error: %v", err)
	}
	want := []string{"/opt/codex/zsh", "-lc", "echo hello"}
	if resolved.ShellType != ShellZsh || !stringSlicesEqual(resolved.Command, want) {
		t.Fatalf("resolved = %#v, want %v", resolved, want)
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

func TestBuildShellRequestForcesPowerShellUTF8OutputLikeRust(t *testing.T) {
	req, err := BuildShellRequest(&ExecCommandArgs{
		Cmd: "Get-Content -LiteralPath .\\unicode.txt -Raw -Encoding UTF8",
	}, &Shell{Type: ShellPowerShell, Path: "powershell.exe"}, ShellValidationOptions{
		ApprovalPolicy: sandbox.ApprovalNever,
		CWD:            t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildShellRequest returned error: %v", err)
	}
	wantScript := shellutil.UTF8OutputPrefix + "Get-Content -LiteralPath .\\unicode.txt -Raw -Encoding UTF8"
	want := []string{"powershell.exe", "-NoProfile", "-Command", wantScript}
	if !reflect.DeepEqual(req.Command, want) {
		t.Fatalf("Command = %#v, want %#v", req.Command, want)
	}
	if req.HookCommand != "Get-Content -LiteralPath .\\unicode.txt -Raw -Encoding UTF8" {
		t.Fatalf("HookCommand = %q", req.HookCommand)
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

func TestBuildShellRequestRejectsJustificationWithoutExplicitSandboxPermissionsLikeRust(t *testing.T) {
	const want = "`justification` requires an explicit `sandbox_permissions`; use `sandbox_permissions: \"require_escalated\"` for unsandboxed execution, or omit `justification`."
	for _, payload := range []string{
		`{"cmd":"echo should not run","justification":"Allow this command"}`,
		`{"cmd":"echo should not run","justification":""}`,
	} {
		var args ExecCommandArgs
		if err := json.Unmarshal([]byte(payload), &args); err != nil {
			t.Fatal(err)
		}
		if _, err := BuildShellRequest(&args, &Shell{Type: ShellBash, Path: "/bin/bash"}, ShellValidationOptions{CWD: t.TempDir()}); err == nil || err.Error() != want {
			t.Fatalf("BuildShellRequest() error = %v, want %q", err, want)
		}
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

func TestBuildShellRequestCarriesWindowsSandboxRuntimeOptions(t *testing.T) {
	profile := sandbox.WorkspaceWritePermissionProfile()
	req, err := BuildShellRequest(&ExecCommandArgs{Cmd: "echo ok"}, &Shell{Type: ShellBash, Path: "/bin/sh"}, ShellValidationOptions{
		CWD:                          t.TempDir(),
		PermissionProfileID:          sandbox.BuiltInPermissionProfileWorkspace,
		PermissionProfile:            &profile,
		WindowsSandboxLevel:          sandbox.WindowsSandboxElevated,
		WindowsSandboxPrivateDesktop: true,
	})
	if err != nil {
		t.Fatalf("BuildShellRequest() error = %v", err)
	}
	if req.WindowsSandboxLevel != sandbox.WindowsSandboxElevated || !req.WindowsSandboxPrivateDesktop {
		t.Fatalf("Windows sandbox options = %q/%t", req.WindowsSandboxLevel, req.WindowsSandboxPrivateDesktop)
	}
}

func TestBuildShellRequestPreservesDeniedReadsForEscalationLikeRust(t *testing.T) {
	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: "**/*.env"},
		Access: sandbox.FileSystemAccessDeny,
	}}
	req, err := BuildShellRequest(&ExecCommandArgs{
		Cmd:                "cat .env",
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
	if req.SandboxPermissions != sandbox.SandboxPermissionsUseDefault {
		t.Fatalf("SandboxPermissions = %q, want use_default to preserve deny-read", req.SandboxPermissions)
	}
	if req.PermissionProfile == nil || !req.PermissionProfile.HasDenyReadEntries() {
		t.Fatalf("PermissionProfile lost deny-read entries: %#v", req.PermissionProfile)
	}
	if req.PermissionProfileID == sandbox.BuiltInPermissionProfileDangerFullAccess || req.PermissionProfile.Disabled {
		t.Fatalf("PermissionProfile = %#v id %q, should not escalate to full access", req.PermissionProfile, req.PermissionProfileID)
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

func TestBuildShellRequestAdditionalPermissionsPreserveCanonicalProfileLikeRust(t *testing.T) {
	canonical := `{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"read"},{"path":{"type":"glob_pattern","pattern":"**/*.env"},"access":"deny"}]},"network":"restricted"}`
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(canonical)
	if err != nil {
		t.Fatalf("ParseRuntimePermissionProfileJSON() error = %v", err)
	}
	network := true
	extra := filepath.Join(t.TempDir(), "cache")
	req, err := BuildShellRequest(&ExecCommandArgs{
		Cmd: "go test ./...", SandboxPermissions: sandbox.SandboxPermissionsWithAdditionalPermissions,
		AdditionalPermissions: &sandbox.AdditionalPermissionProfile{Network: &network, FileSystem: []string{extra}},
	}, &Shell{Type: ShellBash, Path: "/bin/bash"}, ShellValidationOptions{
		AdditionalPermissionsAllowed: true, ApprovalPolicy: sandbox.ApprovalOnRequest, CWD: t.TempDir(),
		PermissionProfileID: "canonical", PermissionProfile: profile, PermissionsPreapproved: true,
	})
	if err != nil {
		t.Fatalf("BuildShellRequest() error = %v", err)
	}
	raw, err := sandbox.RuntimePermissionProfileJSON(*req.PermissionProfile)
	if err != nil {
		t.Fatalf("RuntimePermissionProfileJSON() error = %v", err)
	}
	escapedExtra := strings.ReplaceAll(filepath.Clean(extra), `\`, `\\`)
	for _, want := range []string{`"kind":"root"`, `"access":"deny"`, `"network":"enabled"`, escapedExtra} {
		if !strings.Contains(raw, want) {
			t.Fatalf("canonical profile missing %q: %s", want, raw)
		}
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

func stringSlicesEqual(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
