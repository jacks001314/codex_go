package execserver

import (
	"os"
	"path/filepath"
	"testing"
)

// Rust #49098 adds Windows RPC regression coverage for lowercase and mixed-case
// PowerShell aliases with a restricted server PATH
// (codex-rs/exec-server/tests/exec_process/windows_sandbox.rs). Those tests are
// Windows-only; the checks below freeze the same resolution rules on any
// platform.
func TestWindowsSandboxPowerShellFallbackLikeRust(t *testing.T) {
	compatible := filepath.Join(t.TempDir(), "pwsh.exe")
	if err := os.WriteFile(compatible, []byte("stub"), 0o700); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("PATH", filepath.Dir(compatible))

	storePwsh := `C:\Users\codex\AppData\Local\Microsoft\WindowsApps\pwsh.exe`
	storePowerShellMixedCase := `C:\Users\codex\AppData\Local\Microsoft\WindowsApps\POWERSHELL.EXE`
	unpackaged := `C:\Program Files\PowerShell\7\pwsh.exe`

	cases := []struct {
		name    string
		program string
		level   string
		want    string
	}{
		{"elevated_store_pwsh", storePwsh, windowsSandboxLevelElevated, compatible},
		{"mxc_store_pwsh", storePwsh, windowsSandboxLevelMxc, compatible},
		{"elevated_mixed_case_store_alias", storePowerShellMixedCase, windowsSandboxLevelElevated, compatible},
		{"disabled_keeps_program", storePwsh, windowsSandboxLevelDisabled, storePwsh},
		{"restricted_token_keeps_program", storePwsh, windowsSandboxLevelRestrictedToken, storePwsh},
		{"non_powershell_keeps_program", "/usr/bin/bash", windowsSandboxLevelElevated, "/usr/bin/bash"},
		{"unpackaged_powershell_keeps_program", unpackaged, windowsSandboxLevelElevated, unpackaged},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := windowsSandboxPowerShellProgram(testCase.program, testCase.level); got != testCase.want {
				t.Fatalf("windowsSandboxPowerShellProgram(%q, %q) = %q, want %q", testCase.program, testCase.level, got, testCase.want)
			}
		})
	}
}

// The fallback is conditional on a discoverable compatible executable: a Store
// path stays untouched when no unpackaged PowerShell can be found.
func TestWindowsSandboxPowerShellFallbackWithoutCompatibleShellLikeRust(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	storePwsh := `C:\Users\codex\AppData\Local\Microsoft\WindowsApps\pwsh.exe`
	for _, level := range []string{windowsSandboxLevelElevated, windowsSandboxLevelMxc} {
		if got := windowsSandboxPowerShellProgram(storePwsh, level); got != storePwsh {
			t.Fatalf("level %q: got %q, want the requested program unchanged", level, got)
		}
	}
}

// The second discovery pass covers a Windows host that only ships the built-in
// Windows PowerShell.
func TestWindowsSandboxPowerShellFallbackUsesBuiltInPowerShellLikeRust(t *testing.T) {
	dir := t.TempDir()
	compatible := filepath.Join(dir, "powershell.exe")
	if err := os.WriteFile(compatible, []byte("stub"), 0o700); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv("PATH", dir)

	storePowerShell := `C:\Users\codex\AppData\Local\Microsoft\WindowsApps\powershell.exe`
	if got := windowsSandboxPowerShellProgram(storePowerShell, windowsSandboxLevelMxc); got != compatible {
		t.Fatalf("windowsSandboxPowerShellProgram() = %q, want %q", got, compatible)
	}
}

// WindowsApps also contains valid Codex frameworks (Rust shell_detect.rs), so
// only PowerShell-shaped components count as an inaccessible Store shell.
func TestIsInaccessibleWindowsAppsPowerShellPathLikeRust(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`C:\Users\codex\AppData\Local\Microsoft\WindowsApps\pwsh.exe`, true},
		{`C:/Users/codex/AppData/Local/Microsoft/WindowsApps/PowerShell.EXE`, true},
		{`C:\Users\codex\AppData\Local\Microsoft\WindowsApps\Microsoft.PowerShell_8wekyb3d8bbwe\pwsh.exe`, true},
		{`C:\Users\codex\AppData\Local\Microsoft\WindowsApps\Microsoft.WindowsTerminal_8wekyb3d8bbwe\wt.exe`, false},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, false},
		{`C:\WindowsApps`, false},
	}
	for _, testCase := range cases {
		if got := isInaccessibleWindowsAppsPowerShellPath(testCase.path); got != testCase.want {
			t.Fatalf("isInaccessibleWindowsAppsPowerShellPath(%q) = %v, want %v", testCase.path, got, testCase.want)
		}
	}
}

func TestWindowsPowerShellProgramStemLikeRust(t *testing.T) {
	cases := map[string]string{
		`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`: "powershell",
		`pwsh.exe`:                           "pwsh",
		`PWSH.EXE`:                           "PWSH",
		`/usr/local/bin/pwsh`:                "pwsh",
		`C:\Program Files\PowerShell\7\pwsh`: "pwsh",
		`/`:                                  "",
		`/usr/bin/env`:                       "env",
	}
	for program, want := range cases {
		if got := windowsPowerShellProgramStem(program); got != want {
			t.Fatalf("windowsPowerShellProgramStem(%q) = %q, want %q", program, got, want)
		}
	}
}
