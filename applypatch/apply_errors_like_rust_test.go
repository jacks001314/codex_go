package applypatch

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runApplyCLI runs the patch CLI the way `codex --codex-run-as-apply-patch`
// does and returns the exit code with the captured stderr.
func runApplyCLI(t *testing.T, dir, patch string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := RunCLI([]string{patch}, strings.NewReader(""), &stdout, &stderr, dir)
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	return code, stderr.String()
}

// Delete and write failures carry an anyhow context in 0.166 and the CLI prints
// only that context line, while read failures print their io cause inline.
func TestApplyDeleteDirectoryPrintsOnlyRustContextLine(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dir")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	code, stderr := runApplyCLI(t, dir, "*** Begin Patch\n*** Delete File: dir\n*** End Patch")
	if code != 1 {
		t.Fatalf("code = %d, want 1 (stderr %q)", code, stderr)
	}
	if want := "Failed to delete file " + target + "\n"; stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestApplyWriteFailuresPrintOnlyRustContextLine(t *testing.T) {
	dir := t.TempDir()
	taken := filepath.Join(dir, "taken")
	if err := os.Mkdir(taken, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	code, stderr := runApplyCLI(t, dir, "*** Begin Patch\n*** Add File: taken\n+x\n*** End Patch")
	if code != 1 {
		t.Fatalf("code = %d, want 1 (stderr %q)", code, stderr)
	}
	if want := "Failed to write file " + taken + "\n"; stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}

	parent := filepath.Join(dir, "sub")
	if err := os.WriteFile(parent, []byte("x\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	code, stderr = runApplyCLI(t, dir, "*** Begin Patch\n*** Add File: sub/x.txt\n+hi\n*** End Patch")
	if code != 1 {
		t.Fatalf("code = %d, want 1 (stderr %q)", code, stderr)
	}
	want := "Failed to create parent directories for " + filepath.Join(parent, "x.txt") + "\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestApplyUpdateDirectoryReportsRustReadError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dir")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	code, stderr := runApplyCLI(t, dir, "*** Begin Patch\n*** Update File: dir\n@@\n-a\n+b\n*** End Patch")
	if code != 1 {
		t.Fatalf("code = %d, want 1 (stderr %q)", code, stderr)
	}
	want := "Failed to read file to update " + target + ": " + rustIOMessage(rustDirectoryReadError()) + "\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}
