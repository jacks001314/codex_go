//go:build linux

package tool

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox/linuxsandbox"
)

// TestMain lets this package's test binary act as the production
// codex-linux-sandbox helper for the sandboxed capture test below. Go's test
// driver always passes `-test.*` flags, while the helper argv starts with
// `--sandbox-policy-cwd`, `--apply-seccomp-then-exec`, or a proxy bridge flag,
// so the two invocations cannot be confused.
func TestMain(m *testing.M) {
	if args := os.Args[1:]; linuxSandboxHelperInvocation(args) {
		os.Exit(linuxsandbox.RunHelper(args, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func linuxSandboxHelperInvocation(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "--sandbox-policy-cwd", "--apply-seccomp-then-exec", "--proxy-host-bridge", "--proxy-local-bridge":
		return true
	default:
		return false
	}
}

// prepareSnapshotSandboxHelper puts a `codex-linux-sandbox` alias for this test
// binary first on PATH. The temporary directory is visible inside bubblewrap,
// while a helper installed under the user's home may not be: bwrap execs the
// inner helper through PATH, so a home-directory install makes the capture fail
// with `execvp codex-linux-sandbox: No such file or directory`.
func prepareSnapshotSandboxHelper(t *testing.T) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	dir := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(dir, linuxsandbox.LinuxSandboxArg0)); err != nil {
		t.Fatalf("install sandbox helper alias: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
