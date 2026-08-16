//go:build windows

package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsDevDriveCheckLikeRust(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	flags := func(flags uint32) func(string) (uint32, bool) {
		return func(string) (uint32, bool) { return flags, true }
	}
	cases := []struct {
		name    string
		flags   func(string) (uint32, bool)
		status  CheckStatus
		summary string
	}{
		{"not dev drive", flags(0), CheckStatusWarning, "this worktree is not on a Windows Dev Drive; moving it to a trusted Dev Drive can significantly improve repository and filesystem performance"},
		{"untrusted dev drive", flags(devDriveVolumeFlag), CheckStatusWarning, "the active Git worktree is on an untrusted Windows Dev Drive"},
		{"trusted dev drive", flags(devDriveVolumeFlag | trustedVolumeFlag), CheckStatusOK, "the active Git worktree is on a trusted Windows Dev Drive"},
		{"inspection failed", func(string) (uint32, bool) { return 0, false }, CheckStatusWarning, "the active Git worktree's Windows Dev Drive state could not be inspected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := windowsDevDriveCheckFromInputs(repo, tc.flags)
			if check.Status != tc.status {
				t.Fatalf("status = %q, want %q", check.Status, tc.status)
			}
			if check.Summary != tc.summary {
				t.Fatalf("summary = %q, want %q", check.Summary, tc.summary)
			}
		})
	}

	noRepo := t.TempDir()
	check := windowsDevDriveCheckFromInputs(noRepo, flags(0))
	if check.Status != CheckStatusOK || check.Summary != "no Git worktree is active" {
		t.Fatalf("no worktree = %+v", check)
	}
}
