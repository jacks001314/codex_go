package parity

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/applypatch"
)

// TestExecApplyPatchFreeformFixtureMatchesRust is the djalign dynamic-layer
// method-1 shared-fixture differential for the exec freeform apply_patch
// scenario: the Rust exec suite (exec/tests/suite/apply_patch.rs
// test_apply_patch_freeform_tool) drives codex with an add patch followed by
// an update patch and pins the final file contents via
// exec/tests/fixtures/apply_patch_freeform_final.txt. Go applies the same two
// freeform patches through its apply_patch CLI and must produce byte-identical
// final content.
//
// The Rust side is pinned by name and fixture path: the test verifies the
// #[tokio::test] fn still exists and compares the final file against the
// committed fixture blob (git cat-file, byte-exact) so upstream changes break
// the contract instead of silently drifting. The blob is read from git rather
// than the worktree because the fixture is include_str!-embedded in Rust
// (always LF) while a Windows checkout may normalize it to CRLF.
func TestExecApplyPatchFreeformFixtureMatchesRust(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)
	source := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/exec/tests/suite/apply_patch.rs")
	if !strings.Contains(string(source), "async fn test_apply_patch_freeform_tool()") {
		t.Fatal("Rust test fn test_apply_patch_freeform_tool no longer exists in exec/tests/suite/apply_patch.rs; re-sync the shared fixture")
	}
	want := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/exec/tests/fixtures/apply_patch_freeform_final.txt")

	freeformAddPatch := "*** Begin Patch\n" +
		"*** Add File: app.py\n" +
		"+class BaseClass:\n" +
		"+  def method():\n" +
		"+    return False\n" +
		"*** End Patch"
	freeformUpdatePatch := "*** Begin Patch\n" +
		"*** Update File: app.py\n" +
		"@@  def method():\n" +
		"-    return False\n" +
		"+\n" +
		"+    return True\n" +
		"*** End Patch"

	tmp := t.TempDir()
	for _, patch := range []string{freeformAddPatch, freeformUpdatePatch} {
		var stdout, stderr bytes.Buffer
		if code := applypatch.RunCLI([]string{patch}, nil, &stdout, &stderr, tmp); code != 0 {
			t.Fatalf("apply_patch exit code = %d (stderr: %s)", code, stderr.String())
		}
	}
	got, err := os.ReadFile(filepath.Join(tmp, "app.py"))
	if err != nil {
		t.Fatalf("ReadFile(app.py) after freeform sequence: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("final app.py differs from Rust fixture:\n--- got ---\n%s\n--- want (exec/tests/fixtures/apply_patch_freeform_final.txt) ---\n%s", got, want)
	}
}
