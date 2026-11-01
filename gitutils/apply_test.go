package gitutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitCommand(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repo
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func TestApplyPatchAppliesDiffInRepository(t *testing.T) {
	repo := t.TempDir()
	runGitCommand(t, repo, "init", "-q")
	runGitCommand(t, repo, "config", "core.autocrlf", "false")
	path := filepath.Join(repo, "hello.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	runGitCommand(t, repo, "add", ".")
	runGitCommand(t, repo, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-q", "-m", "init")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	diff := runGitCommand(t, repo, "diff")
	runGitCommand(t, repo, "checkout", "--", "hello.txt")

	result, err := ApplyPatch(&ApplyRequest{CWD: repo, Diff: diff})
	if err != nil {
		t.Fatalf("ApplyPatch error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, stderr = %q", result.ExitCode, result.Stderr)
	}
	// Rust's apply_add_success only contracts the exit code and the file effect:
	// some git versions print no "Applied patch" line for a clean --3way apply.
	// Path grouping is covered by TestParseGitApplyOutputLikeRust.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if string(data) != "hello world\n" {
		t.Fatalf("file = %q", data)
	}
}

func TestApplyPatchReportsFailure(t *testing.T) {
	repo := t.TempDir()
	runGitCommand(t, repo, "init", "-q")
	runGitCommand(t, repo, "config", "core.autocrlf", "false")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	runGitCommand(t, repo, "add", ".")
	runGitCommand(t, repo, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("different\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	diff := runGitCommand(t, repo, "diff")
	runGitCommand(t, repo, "checkout", "--", "hello.txt")
	if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("conflict\n"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	result, err := ApplyPatch(&ApplyRequest{CWD: repo, Diff: diff})
	if err != nil {
		t.Fatalf("ApplyPatch error = %v", err)
	}
	if result.ExitCode == 0 {
		t.Fatalf("ExitCode = 0, want failure; stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestApplyPatchRejectsNonRepository(t *testing.T) {
	_, err := ApplyPatch(&ApplyRequest{CWD: t.TempDir(), Diff: "diff --git a/x b/x\n"})
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("ApplyPatch error = %v", err)
	}
}

func TestParseGitApplyOutputLikeRust(t *testing.T) {
	applied, skipped, conflicted := ParseGitApplyOutput("Applied patch hello.txt cleanly.\n", "")
	if len(applied) != 1 || applied[0] != "hello.txt" || len(skipped) != 0 || len(conflicted) != 0 {
		t.Fatalf("applied=%#v skipped=%#v conflicted=%#v", applied, skipped, conflicted)
	}
	applied, skipped, conflicted = ParseGitApplyOutput("", "error: patch failed: a.txt:1\nerror: a.txt: patch does not apply\n")
	if len(applied) != 0 || len(skipped) != 1 || skipped[0] != "a.txt" || len(conflicted) != 0 {
		t.Fatalf("applied=%#v skipped=%#v conflicted=%#v", applied, skipped, conflicted)
	}
	applied, skipped, conflicted = ParseGitApplyOutput("Applied patch to 'dir/a b.txt' cleanly.\n", "")
	if len(applied) != 1 || applied[0] != "dir/a b.txt" || len(skipped) != 0 {
		t.Fatalf("quoted path: applied=%#v skipped=%#v conflicted=%#v", applied, skipped, conflicted)
	}
	applied, skipped, conflicted = ParseGitApplyOutput("Checking patch c.txt...\nFailed to perform three-way merge...\n", "")
	if len(applied) != 0 || len(skipped) != 1 || skipped[0] != "c.txt" {
		t.Fatalf("three-way failure: applied=%#v skipped=%#v conflicted=%#v", applied, skipped, conflicted)
	}
	applied, skipped, conflicted = ParseGitApplyOutput("Checking patch b.txt...\nApplied patch b.txt with conflicts.\n", "")
	if len(conflicted) != 1 || conflicted[0] != "b.txt" || len(applied) != 0 {
		t.Fatalf("applied=%#v skipped=%#v conflicted=%#v", applied, skipped, conflicted)
	}
}

func TestExtractPathsFromPatch(t *testing.T) {
	paths := ExtractPathsFromPatch("diff --git a/one.txt b/one.txt\n--- a/one.txt\n+++ b/one.txt\ndiff --git a/two.txt b/two.txt\n")
	if len(paths) != 2 || paths[0] != "one.txt" || paths[1] != "two.txt" {
		t.Fatalf("paths = %#v", paths)
	}
}
