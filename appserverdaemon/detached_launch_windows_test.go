//go:build windows

package appserverdaemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Mirrors Rust #48157's probe: the suspended breakaway launch must succeed for a
// real executable and be terminated and reaped afterwards.
func TestEnsureDetachedLaunchProbesAndReapsSuspendedChild(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	if err := ensureDetachedLaunch(executable); err != nil {
		t.Fatalf("ensureDetachedLaunch(real executable) error = %v", err)
	}

	// A path that cannot be launched reports Rust's plain failure contract, and
	// must never be classified as a job restriction: an inaccessible or corrupt
	// installation stays visible.
	missing := filepath.Join(t.TempDir(), "missing-codex.exe")
	err = ensureDetachedLaunch(missing)
	if err == nil || !strings.Contains(err.Error(), "cannot launch detached daemon") {
		t.Fatalf("ensureDetachedLaunch(missing) error = %v", err)
	}
	if IsDetachedLaunchRestricted(err) {
		t.Fatalf("missing executable classified as a launcher restriction: %v", err)
	}
}

// Mirrors Rust's `DetachedLaunchRestricted` Display and classification contract:
// the error is recognizable by type and carries the build-and-run guidance.
func TestDetachedLaunchRestrictedErrorContract(t *testing.T) {
	var nilErr *DetachedLaunchRestrictedError
	if nilErr.Error() != "this Windows launcher prevents background processes from outliving it (for example, cargo run); build and run codex.exe directly to use the background server" {
		t.Fatalf("restriction message = %q", nilErr.Error())
	}
	restricted := &DetachedLaunchRestrictedError{err: os.ErrPermission}
	if !IsDetachedLaunchRestricted(restricted) {
		t.Fatal("IsDetachedLaunchRestricted(restricted) = false")
	}
	if !errors.Is(restricted, os.ErrPermission) {
		t.Fatal("restriction must unwrap to the underlying launch error")
	}
	if IsDetachedLaunchRestricted(nil) {
		t.Fatal("IsDetachedLaunchRestricted(nil) = true")
	}
}

// The managed-binary preflight runs the probe, so an unlaunchable managed path
// fails before the daemon lifecycle touches a running daemon.
func TestEnsureManagedCodexBinProbesTheLaunch(t *testing.T) {
	notExecutable := filepath.Join(t.TempDir(), "codex-not-a-binary")
	if err := os.WriteFile(notExecutable, []byte("not a program"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := EnsureManagedCodexBin(notExecutable)
	if err == nil || !strings.Contains(err.Error(), "cannot launch detached daemon") {
		t.Fatalf("EnsureManagedCodexBin(not executable) error = %v", err)
	}

	// A real executable passes the preflight.
	executable, execErr := os.Executable()
	if execErr != nil {
		t.Fatalf("os.Executable() error = %v", execErr)
	}
	if err := EnsureManagedCodexBin(executable); err != nil {
		t.Fatalf("EnsureManagedCodexBin(real executable) error = %v", err)
	}
}
