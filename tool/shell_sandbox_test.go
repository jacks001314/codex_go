package tool

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

func TestElevatedSandboxFilterRejectsStoreAndScriptPowerShellPaths(t *testing.T) {
	// Store PowerShell paths are inaccessible to the elevated sandbox account.
	for _, path := range []string{
		`C:\Program Files\WindowsApps\Microsoft.PowerShell_7.6.4.0_x64__8wekyb3d8bbwe\pwsh.exe`,
		`C:\Program Files\WindowsApps\Microsoft.PowerShellPreview_8wekyb3d8bbwe\pwsh.exe`,
		`C:\Users\user\AppData\Local\Microsoft\WindowsApps\pwsh.exe`,
		`C:\Users\user\AppData\Local\Microsoft\WindowsApps\powershell.exe`,
		`C:\Users\user\AppData\Local\Microsoft\WindowsApps\Microsoft.PowerShell_8wekyb3d8bbwe\pwsh.exe`,
		`C:\PROGRAM FILES\WINDOWSAPPS\MICROSOFT.POWERSHELL\PWSH.EXE`,
	} {
		if !isInaccessibleWindowsAppsPowerShellPath(path) {
			t.Fatalf("isInaccessibleWindowsAppsPowerShellPath(%q) = false, want true", path)
		}
		if isElevatedSandboxCompatiblePowerShellPath(path) {
			t.Fatalf("isElevatedSandboxCompatiblePowerShellPath(%q) = true, want false", path)
		}
	}
	// A non-exe script is rejected even though it is not a Store launcher.
	if isElevatedSandboxCompatiblePowerShellPath(`C:\portable\pwsh.cmd`) {
		t.Fatalf("isElevatedSandboxCompatiblePowerShellPath(pwsh.cmd) = true, want false")
	}

	// Compatible native/portable PowerShell paths are accepted.
	for _, path := range []string{
		`C:\Program Files\PowerShell\7\pwsh.exe`,
		`C:\Program Files\WindowsApps\OpenAI.CodexPrimaryRuntime.v26-813-10124-0_26.813.10124.0_x64__3k8sg7r9htsxt\dependencies\native\powershell\pwsh.exe`,
		`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
		`C:\Users\user\.cache\codex-runtimes\codex-primary-runtime\dependencies\native\powershell\pwsh.exe`,
		`C:\portable\NotWindowsApps\pwsh.EXE`,
	} {
		if isInaccessibleWindowsAppsPowerShellPath(path) {
			t.Fatalf("isInaccessibleWindowsAppsPowerShellPath(%q) = true, want false", path)
		}
		if !isElevatedSandboxCompatiblePowerShellPath(path) {
			t.Fatalf("isElevatedSandboxCompatiblePowerShellPath(%q) = false, want true", path)
		}
	}
}

func TestPreparePowerShellCommandForElevatedWindowsSandbox(t *testing.T) {
	base := []string{`powershell.exe`, "-Command", "Write-Output ok"}
	// Local elevated sandbox PowerShell gets -NoProfile inserted.
	got := preparePowerShellCommandForElevatedWindowsSandbox(
		base, ShellPowerShell, true, sandbox.WindowsSandboxElevated, false)
	if !equalStringSlices(got, []string{`powershell.exe`, "-NoProfile", "-Command", "Write-Output ok"}) {
		t.Fatalf("elevated command = %#v", got)
	}
	// Existing -NoProfile is preserved.
	withProfile := []string{`powershell.exe`, "-NoProfile", "-Command", "x"}
	if got := preparePowerShellCommandForElevatedWindowsSandbox(
		withProfile, ShellPowerShell, true, sandbox.WindowsSandboxElevated, false); !equalStringSlices(got, withProfile) {
		t.Fatalf("-NoProfile command = %#v", got)
	}
	// Non-PowerShell, non-elevated, remote, and non-sandboxed commands are unchanged.
	for _, tc := range []struct {
		command []string
		shell   ShellType
		level   sandbox.WindowsSandboxLevel
		remote  bool
		sandbox bool
	}{
		{[]string{`bash`, "-c", "echo hi"}, ShellBash, sandbox.WindowsSandboxElevated, false, true},
		{base, ShellPowerShell, sandbox.WindowsSandboxDisabled, false, true},
		{base, ShellPowerShell, sandbox.WindowsSandboxElevated, true, true},
		{base, ShellPowerShell, sandbox.WindowsSandboxElevated, false, false},
	} {
		if got := preparePowerShellCommandForElevatedWindowsSandbox(
			tc.command, tc.shell, tc.sandbox, tc.level, tc.remote); !equalStringSlices(got, tc.command) {
			t.Fatalf("preparePowerShellCommandForElevatedWindowsSandbox(%#v, %s, %s, %v) = %#v, want unchanged",
				tc.command, tc.shell, tc.level, tc.remote, got)
		}
	}
}

func TestFallbackPowerShellShellForElevatedWindowsSandboxNonStoreReturnsNil(t *testing.T) {
	// A native/path outside WindowsApps triggers no replacement.
	if shell := fallbackPowerShellShellForElevatedWindowsSandbox(`C:\Program Files\PowerShell\7\pwsh.exe`); shell != nil {
		t.Fatalf("fallback(native) = %#v, want nil", shell)
	}
	if shell := fallbackPowerShellShellForElevatedWindowsSandbox(``); shell != nil {
		t.Fatalf("fallback(empty) = %#v, want nil", shell)
	}
}

// Mirrors Rust #49019: MXC gets the local Store-PowerShell fallback but the
// elevated-only -NoProfile insertion is not applied.
func TestPreparePowerShellCommandForMXCWindowsSandboxLikeRust(t *testing.T) {
	compatible := filepath.Join(t.TempDir(), "pwsh.exe")
	if err := os.WriteFile(compatible, []byte("stub"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(compatible))
	storePowerShell := `C:\Users\user\AppData\Local\Microsoft\WindowsApps\pwsh.exe`
	command := []string{storePowerShell, "-Command", "Write-Output ok"}

	got := preparePowerShellCommandForWindowsSandbox(
		command,
		ShellPowerShell,
		/*sandboxRequested*/ true,
		sandbox.WindowsSandboxMxc,
		/*environmentIsRemote*/ false,
	)
	want := []string{compatible, "-Command", "Write-Output ok"}
	if !equalStringSlices(got, want) {
		t.Fatalf("MXC command = %#v, want %#v", got, want)
	}

	// Elevated still inserts -NoProfile after the replacement.
	elevated := preparePowerShellCommandForWindowsSandbox(
		command,
		ShellPowerShell,
		/*sandboxRequested*/ true,
		sandbox.WindowsSandboxElevated,
		/*environmentIsRemote*/ false,
	)
	want = []string{compatible, "-NoProfile", "-Command", "Write-Output ok"}
	if !equalStringSlices(elevated, want) {
		t.Fatalf("elevated command = %#v, want %#v", elevated, want)
	}

	// Disabled/restricted levels and remote environments stay unchanged.
	for _, level := range []sandbox.WindowsSandboxLevel{sandbox.WindowsSandboxDisabled, sandbox.WindowsSandboxUnelevated} {
		if got := preparePowerShellCommandForWindowsSandbox(
			command, ShellPowerShell, true, level, false,
		); !equalStringSlices(got, command) {
			t.Fatalf("%s command = %#v, want unchanged", level, got)
		}
	}
	if got := preparePowerShellCommandForWindowsSandbox(
		command, ShellPowerShell, true, sandbox.WindowsSandboxMxc, true,
	); !equalStringSlices(got, command) {
		t.Fatalf("remote MXC command = %#v, want unchanged", got)
	}
}
