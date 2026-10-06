//go:build windows

package appserverdaemon

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// TestDaemonJunctionCanBeCreatedRetargetedAndReplacedLikeRust mirrors Rust
// #50802: the daemon's `current` selection falls back to cmd.exe's `mklink /J`
// when a Windows policy denies in-process reparse-point mutation, while native
// retargeting still works when it is allowed. The root path carries spaces and
// shell metacharacters so the environment-variable handoff is exercised, and no
// standalone CLI installation may appear.
func TestDaemonJunctionCanBeCreatedRetargetedAndReplacedLikeRust(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "home & 100% ready!", "packages", "app-server-daemon")
	denied := func(current, release string) error {
		return fmt.Errorf("failed to retarget managed daemon junction: %w", fs.ErrPermission)
	}
	for _, testCase := range []struct {
		version string
		denied  bool
	}{
		{"first", true},
		{"second", false},
		{"third", true},
	} {
		release := filepath.Join(root, releasesDirName, testCase.version)
		if err := os.MkdirAll(release, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", release, err)
		}
		var err error
		if testCase.denied {
			err = selectDaemonReleaseWith(root, release, denied)
		} else {
			err = selectDaemonRelease(root, release)
		}
		if err != nil {
			t.Fatalf("selectDaemonRelease(%s) error = %v", testCase.version, err)
		}
		want := canonicalPath(release)
		got := canonicalPath(filepath.Join(root, "current"))
		if want == "" || got == "" {
			t.Fatalf("canonicalPath after %s = (%q, %q)", testCase.version, got, want)
		}
		if got != want {
			t.Fatalf("current after %s = %q, want %q", testCase.version, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "standalone")); !os.IsNotExist(err) {
		t.Fatalf("standalone installation appeared: %v", err)
	}
}

// TestIsPermissionDeniedRecognizesAccessDenialsLikeRust pins the fallback
// predicate: an access-denied io error or Windows error triggers the mklink
// fallback, other failures propagate.
func TestIsPermissionDeniedRecognizesAccessDenialsLikeRust(t *testing.T) {
	if !isPermissionDenied(fmt.Errorf("retarget: %w", fs.ErrPermission)) {
		t.Fatal("fs.ErrPermission should trigger the fallback")
	}
	if !isPermissionDenied(fmt.Errorf("retarget: %w", windows.ERROR_ACCESS_DENIED)) {
		t.Fatal("Windows access denied should trigger the fallback")
	}
	if isPermissionDenied(fmt.Errorf("retarget: %w", os.ErrNotExist)) {
		t.Fatal("a missing path must not trigger the fallback")
	}
}
