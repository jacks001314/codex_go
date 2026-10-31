//go:build windows

package windowssandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
)

// TestWrapperRunsWithCallerOwnedDesktopLikeRust exercises Rust #44658 end to
// end: the caller selects and caches the desktop, the wrapper opens that name,
// and the sandboxed child still starts (a private desktop the restricted token
// cannot open fails with STATUS_DLL_INIT_FAILED).
func TestWrapperRunsWithCallerOwnedDesktopLikeRust(t *testing.T) {
	if _, err := os.Stat(filepath.Join(os.Getenv("SystemRoot"), "System32", "where.exe")); err != nil {
		t.Skipf("where.exe unavailable: %v", err)
	}
	workspace := t.TempDir()
	codexHome := t.TempDir()
	profile := coresandbox.ReadOnlyPermissionProfile()
	args, err := CreateWindowsSandboxCommandArgsForPermissionProfile(WindowsSandboxCommandArgsRequest{
		Command:             []string{"where.exe", "cmd"},
		CommandCWD:          workspace,
		WorkspaceRoots:      []string{workspace},
		Env:                 map[string]string{"SystemRoot": os.Getenv("SystemRoot"), "TEMP": os.Getenv("TEMP")},
		PermissionProfile:   &profile,
		WindowsSandboxLevel: WindowsSandboxLevelRestrictedToken,
		CodexHome:           codexHome,
		PreparePrivateDesktop: true,
	})
	if err != nil {
		if IsUnsupported(err) {
			t.Skipf("restricted-token sandbox unsupported on this host: %v", err)
		}
		t.Fatalf("CreateWindowsSandboxCommandArgsForPermissionProfile() error = %v", err)
	}
	var stdout bytes.Buffer
	exitCode, err := RunWindowsSandboxWrapperExitCode(args, nil, &stdout, os.Stderr)
	if err != nil {
		if IsUnsupported(err) {
			t.Skipf("restricted-token sandbox unsupported on this host: %v", err)
		}
		t.Fatalf("RunWindowsSandboxWrapperExitCode() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exit code = %d, stdout=%q", exitCode, stdout.String())
	}
	if !strings.Contains(strings.ToLower(stdout.String()), "cmd.exe") {
		t.Fatalf("stdout = %q, want the cmd.exe path", stdout.String())
	}
}
