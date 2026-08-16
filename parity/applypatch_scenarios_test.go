package parity

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"codex_go/applypatch"
)

// TestApplyPatchScenariosMatchRustFixtures runs the Rust apply-patch scenario
// corpus (apply-patch/tests/fixtures/scenarios) through Go's applypatch and
// compares the resulting file tree byte-for-byte with the Rust expected tree.
//
// This is the shared-fixture double-run (djalign dynamic-layer method 1):
// both implementations eat the same committed fixtures, and the observable
// outcome (final filesystem state) must be identical. Rust's own runner does
// not assert on the apply-patch exit status (scenarios are specified purely
// by final state), so Go mirrors that: rejection scenarios pass when the tree
// is unchanged, and errors are not treated as failures by themselves.
func TestApplyPatchScenariosMatchRustFixtures(t *testing.T) {
	root := rustSnapshotRoot(t)
	scenariosDir := filepath.Join(root, "apply-patch", "tests", "fixtures", "scenarios")
	entries, err := os.ReadDir(scenariosDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", scenariosDir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) != 25 {
		t.Fatalf("Rust apply-patch scenario count = %d, want 25 (pinned baseline)", len(names))
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			runApplyPatchScenario(t, filepath.Join(scenariosDir, name))
		})
	}
}

func runApplyPatchScenario(t *testing.T, dir string) {
	t.Helper()
	tmp := t.TempDir()

	// Copy the input tree into the workspace (absent input = empty workspace).
	inputDir := filepath.Join(dir, "input")
	if info, err := os.Stat(inputDir); err == nil && info.IsDir() {
		if err := copyTree(inputDir, tmp); err != nil {
			t.Fatalf("copy input tree: %v", err)
		}
	}

	patch, err := os.ReadFile(filepath.Join(dir, "patch.txt"))
	if err != nil {
		t.Fatalf("ReadFile(patch.txt): %v", err)
	}

	// Run the apply_patch CLI in the workspace with Rust's preserve-line-endings
	// mode (the Rust runner sets CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS=1 and
	// passes the patch as a single argument). The exit code is intentionally
	// not asserted: scenarios are defined purely by final filesystem state,
	// exactly like Rust's runner.
	t.Setenv(applypatch.PreserveLineEndingsEnvVar, "1")
	var stdout, stderr bytes.Buffer
	_ = applypatch.RunCLI([]string{string(patch)}, strings.NewReader(""), &stdout, &stderr, tmp)

	actual := snapshotTree(t, tmp)
	expected := snapshotTree(t, filepath.Join(dir, "expected"))
	if len(actual) != len(expected) {
		t.Fatalf("final tree entry count = %d, want %d\nactual=%v\nexpected=%v",
			len(actual), len(expected), actual, expected)
	}
	for rel, want := range expected {
		got, ok := actual[rel]
		if !ok {
			t.Fatalf("missing expected entry %q", rel)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("entry %q differs from expected fixture", rel)
		}
	}
	for rel := range actual {
		if _, ok := expected[rel]; !ok {
			t.Fatalf("unexpected extra entry %q", rel)
		}
	}
}

// snapshotTree mirrors Rust's snapshot_dir: a sorted map of relative path to
// file bytes (directories are recorded as empty markers so empty dirs are
// part of the observable tree).
func snapshotTree(t *testing.T, root string) map[string][]byte {
	t.Helper()
	snap := map[string][]byte{}
	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			snap[rel] = nil
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// Rust follows symlinks via fs::metadata; resolve for the snapshot.
			target, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snap[rel] = target
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snap[rel] = data
		return nil
	}
	if err := filepath.WalkDir(root, walk); err != nil {
		t.Fatalf("WalkDir(%s): %v", root, err)
	}
	return snap
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Type()&fs.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
