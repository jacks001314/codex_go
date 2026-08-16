package parity

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"codex_go/applypatch"
)

// TestApplyPatchDifferentialAgainstRustOracle is the djalign dynamic-layer
// method-5 differential: generate a deterministic corpus of random patches and
// workspaces, apply each with the Rust apply_patch binary (the oracle) and
// with Go's apply_patch CLI, and require the same exit status and the same
// final file tree byte-for-byte.
//
// The oracle is a release build of codex-rs/apply-patch; the test skips when
// the binary is absent (e.g. CI without a Rust checkout/build) so the corpus
// is still regenerable locally and on the daily sync.
func TestApplyPatchDifferentialAgainstRustOracle(t *testing.T) {
	oracle := rustApplyPatchBinary(t)
	cases := differentialApplyPatchCases(t)
	for i, tc := range cases {
		t.Run(fmt.Sprintf("case_%04d", i), func(t *testing.T) {
			rustResult := runOracleApplyPatch(t, oracle, tc)
			goResult := runGoApplyPatch(t, tc)
			if (rustResult.exitNonZero) != (goResult.exitNonZero) {
				t.Fatalf("exit disagreement: rust exit!=0 = %v, go exit!=0 = %v\npatch:\n%s",
					rustResult.exitNonZero, goResult.exitNonZero, tc.patch)
			}
			if len(rustResult.tree) != len(goResult.tree) {
				t.Fatalf("tree entry count mismatch: rust %d, go %d\npatch:\n%s\nrust tree=%v\ngo tree=%v",
					len(rustResult.tree), len(goResult.tree), tc.patch, rustResult.tree, goResult.tree)
			}
			for rel, rustBytes := range rustResult.tree {
				goBytes, ok := goResult.tree[rel]
				if !ok {
					t.Fatalf("entry %q present in rust tree, absent in go\npatch:\n%s", rel, tc.patch)
				}
				if !bytes.Equal(rustBytes, goBytes) {
					t.Fatalf("entry %q differs between rust and go\npatch:\n%s", rel, tc.patch)
				}
			}
		})
	}
}

type differentialApplyPatchCase struct {
	workspace map[string]string // rel path -> content
	patch     string
}

func rustApplyPatchBinary(t *testing.T) string {
	t.Helper()
	if env := os.Getenv("CODEX_RUST_APPLY_PATCH_BIN"); env != "" {
		if _, err := os.Stat(env); err != nil {
			t.Fatalf("CODEX_RUST_APPLY_PATCH_BIN %s: %v", env, err)
		}
		return env
	}
	root := rustSnapshotRoot(t)
	candidates := []string{
		filepath.Join(root, "target", "release", "apply_patch"),
		filepath.Join(root, "target", "release", "apply_patch.exe"),
		filepath.Join(root, "target", "debug", "apply_patch"),
		filepath.Join(root, "target", "debug", "apply_patch.exe"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	t.Skip("Rust apply_patch oracle binary not found; build with: cargo build -p codex-apply-patch --release (in codex-rs)")
	return ""
}

type applyPatchOutcome struct {
	exitNonZero bool
	tree        map[string][]byte
}

func runOracleApplyPatch(t *testing.T, oracle string, tc differentialApplyPatchCase) applyPatchOutcome {
	t.Helper()
	dir := t.TempDir()
	writeDifferentialWorkspace(t, dir, tc.workspace)
	cmd := exec.Command(oracle, tc.patch)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS=1")
	err := cmd.Run()
	exitNonZero := false
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("rust oracle exec: %v", err)
		}
		exitNonZero = true
	}
	return applyPatchOutcome{exitNonZero: exitNonZero, tree: snapshotTree(t, dir)}
}

func runGoApplyPatch(t *testing.T, tc differentialApplyPatchCase) applyPatchOutcome {
	t.Helper()
	dir := t.TempDir()
	writeDifferentialWorkspace(t, dir, tc.workspace)
	t.Setenv(applypatch.PreserveLineEndingsEnvVar, "1")
	var stdout, stderr bytes.Buffer
	code := applypatch.RunCLI([]string{tc.patch}, strings.NewReader(""), &stdout, &stderr, dir)
	return applyPatchOutcome{exitNonZero: code != 0, tree: snapshotTree(t, dir)}
}

func writeDifferentialWorkspace(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(files[name]), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", path, err)
		}
	}
}

// differentialApplyPatchCases builds a deterministic corpus from a seeded PRNG
// so failures are reproducible without network or tokens.
func differentialApplyPatchCases(t *testing.T) []differentialApplyPatchCase {
	t.Helper()
	count := 200
	if env := os.Getenv("APPLY_PATCH_DIFF_CASES"); env != "" {
		if parsed, err := strconv.Atoi(env); err == nil && parsed > 0 {
			count = parsed
		}
	}
	seed := int64(20260816)
	if env := os.Getenv("APPLY_PATCH_DIFF_SEED"); env != "" {
		if parsed, err := strconv.ParseInt(env, 10, 64); err == nil {
			seed = parsed
		}
	}
	rng := rand.New(rand.NewSource(seed))
	var cases []differentialApplyPatchCase
	for i := 0; i < count; i++ {
		cases = append(cases, randomDifferentialCase(rng, i))
	}
	return cases
}

var differentialFileNames = []string{"a.txt", "b.txt", "nested/c.txt", "foo.go", "old.py"}

var differentialContentLines = []string{
	"hello", "world", "one", "two", "three", "",
	"func main() {", "return nil", "line with spaces", "x = 1",
}

func randomDifferentialCase(rng *rand.Rand, index int) differentialApplyPatchCase {
	// Build a workspace of 0-3 files.
	fileCount := rng.Intn(4)
	names := append([]string(nil), differentialFileNames...)
	rng.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	workspace := map[string]string{}
	for i := 0; i < fileCount; i++ {
		workspace[names[i]] = randomContent(rng)
	}

	// Patch: mix of valid and invalid hunk sequences.
	hunkCount := 1 + rng.Intn(3)
	var b strings.Builder
	b.WriteString("*** Begin Patch\n")
	for h := 0; h < hunkCount; h++ {
		kind := rng.Intn(100)
		switch {
		case kind < 40: // update existing file
			name := names[rng.Intn(len(names))]
			b.WriteString("*** Update File: " + name + "\n")
			b.WriteString("@@\n")
			oldLines := strings.Split(workspace[name], "\n")
			if len(oldLines) == 0 || (len(oldLines) == 1 && oldLines[0] == "") {
				oldLines = []string{"missing"}
			}
			target := oldLines[rng.Intn(len(oldLines))]
			if target == "" {
				target = "empty-line"
			}
			b.WriteString("-" + target + "\n")
			b.WriteString("+" + target + " changed\n")
		case kind < 65: // add file
			name := "added_" + strconv.Itoa(index) + "_" + strconv.Itoa(h) + ".txt"
			if rng.Intn(4) == 0 {
				name = "nested/" + name
			}
			b.WriteString("*** Add File: " + name + "\n")
			b.WriteString("+" + differentialContentLines[rng.Intn(len(differentialContentLines))] + "\n")
			if rng.Intn(2) == 0 {
				b.WriteString("+" + differentialContentLines[rng.Intn(len(differentialContentLines))] + "\n")
			}
		case kind < 80: // delete existing file
			name := names[rng.Intn(len(names))]
			b.WriteString("*** Delete File: " + name + "\n")
		case kind < 90: // update + move
			name := names[rng.Intn(len(names))]
			b.WriteString("*** Update File: " + name + "\n")
			b.WriteString("*** Move to: moved_" + strconv.Itoa(index) + "_" + strconv.Itoa(h) + ".txt\n")
			b.WriteString("@@\n")
			oldLines := strings.Split(workspace[name], "\n")
			if len(oldLines) > 0 && oldLines[0] != "" {
				b.WriteString("-" + oldLines[0] + "\n")
				b.WriteString("+" + oldLines[0] + " renamed\n")
			}
		default: // invalid patch constructs (both sides must reject or accept alike)
			switch rng.Intn(4) {
			case 0:
				b.WriteString("*** Update File: missing_file.txt\n@@\n-absent\n+present\n")
			case 1:
				b.WriteString("garbage hunk line\n")
			case 2:
				b.WriteString("*** Add File: bad.txt\nnot-a-plus-line\n")
			case 3:
				b.WriteString("*** Delete File: never_created.txt\n")
			}
		}
	}
	b.WriteString("*** End Patch\n")
	return differentialApplyPatchCase{workspace: workspace, patch: b.String()}
}

func randomContent(rng *rand.Rand) string {
	lineCount := 1 + rng.Intn(3)
	lines := make([]string, 0, lineCount)
	for i := 0; i < lineCount; i++ {
		lines = append(lines, differentialContentLines[rng.Intn(len(differentialContentLines))])
	}
	return strings.Join(lines, "\n") + "\n"
}
