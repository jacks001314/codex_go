package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSystemBwrapWarningRunsLikeRust(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap detection is Linux-only")
	}
	// The result depends on the host (PATH and /proc/version), so this only
	// pins that the warning is one of the documented texts.
	readOnly := ReadOnlyPermissionProfile()
	warning := SystemBwrapWarning(&readOnly)
	if warning == "" {
		return
	}
	for _, known := range []string{missingBwrapWarning, userNamespaceWarning, wsl1BwrapWarning} {
		if warning == known {
			return
		}
	}
	t.Fatalf("SystemBwrapWarning() = %q, want one of the documented texts", warning)
}

func TestSystemBwrapWarningDetectsMissingBwrapLikeRust(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap detection is Linux-only")
	}
	originalPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", originalPath) })
	if err := os.Setenv("PATH", t.TempDir()); err != nil {
		t.Fatalf("Setenv error = %v", err)
	}
	readOnly := ReadOnlyPermissionProfile()
	warning := SystemBwrapWarning(&readOnly)
	if warning != missingBwrapWarning && warning != wsl1BwrapWarning {
		t.Fatalf("warning = %q, want the missing-bubblewrap text (or the WSL1 text)", warning)
	}
}

func TestProbeSystemBwrapUserNamespacesLikeRust(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap is Linux-only")
	}
	directory := t.TempDir()
	writeFakeBwrap(t, filepath.Join(directory, "ok"), "exit 0\n")
	if !probeSystemBwrapUserNamespaces(filepath.Join(directory, "ok"), systemBwrapProbeTimeout) {
		t.Error("a successful launcher must report user namespace access")
	}
	writeFakeBwrap(t, filepath.Join(directory, "denied"),
		"echo 'bwrap: No permissions to create a new namespace' >&2\nexit 1\n")
	if probeSystemBwrapUserNamespaces(filepath.Join(directory, "denied"), systemBwrapProbeTimeout) {
		t.Error("a user-namespace failure must report no access")
	}
	writeFakeBwrap(t, filepath.Join(directory, "other"),
		"echo 'bwrap: Unknown option --argv0' >&2\nexit 1\n")
	if !probeSystemBwrapUserNamespaces(filepath.Join(directory, "other"), systemBwrapProbeTimeout) {
		t.Error("an unrelated failure must not be reported as a namespace problem")
	}
}

func writeFakeBwrap(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}
