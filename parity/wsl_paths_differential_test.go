package parity

import (
	"path/filepath"
	"strings"
	"testing"

	"codex_go/shell"
)

// TestRustWSLPathSamplesRunInGo is the djalign dynamic-layer method-1
// shared-fixture differential for Rust cli/src/wsl_paths.rs: the
// win_to_wsl_basic and normalize_is_noop_on_unix_paths samples drive Go's
// shell.WinPathToWSL / shell.NormalizeForWSL (used by the update command's
// non-Windows branch). The Rust side is pinned by blob content
// (candidateRustTo), so upstream edits break the contract instead of silently
// drifting.
func TestRustWSLPathSamplesRunInGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)

	blob := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/cli/src/wsl_paths.rs")
	source := string(blob)
	if !strings.Contains(source, "win_to_wsl_basic") || !strings.Contains(source, "normalize_is_noop_on_unix_paths") {
		t.Fatal("Rust wsl_paths.rs no longer carries the expected samples; re-sync the shared fixture")
	}

	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "win_to_wsl_basic/backslash", in: `C:\Temp\codex.zip`, want: "/mnt/c/Temp/codex.zip", ok: true},
		{name: "win_to_wsl_basic/forward-slash", in: "D:/Work/codex.tgz", want: "/mnt/d/Work/codex.tgz", ok: true},
		{name: "win_to_wsl_basic/unix-rejected", in: "/home/user/codex", ok: false},
		{name: "normalize_is_noop_on_unix_paths", in: "/home/u/x", want: "/home/u/x", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := shell.WinPathToWSL(tc.in)
			if ok != tc.ok {
				t.Fatalf("WinPathToWSL(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			}
			if tc.ok && got != tc.want {
				t.Fatalf("WinPathToWSL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
