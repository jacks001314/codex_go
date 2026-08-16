package applypatch

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIAppliesPatchArgument(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := RunCLI([]string{"*** Begin Patch\n*** Add File: a.txt\n+hi\n*** End Patch"}, strings.NewReader(""), &stdout, &stderr, dir)
	if code != 0 {
		t.Fatalf("RunCLI code = %d stderr = %q", code, stderr.String())
	}
	if stdout.String() != "Success. Updated the following files:\nA a.txt\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(data) != "hi\n" {
		t.Fatalf("a.txt = %q", string(data))
	}
}

func TestRunCLIReportsError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunCLI([]string{"*** Begin Patch\n*** Delete File: missing.txt\n*** End Patch"}, strings.NewReader(""), &stdout, &stderr, t.TempDir())
	if code == 0 {
		t.Fatal("RunCLI code = 0, want failure")
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Failed to delete file") {
		t.Fatalf("stdout = %q stderr = %q", stdout.String(), stderr.String())
	}
}

// TestRunCLILeavesPartialSuccessOnFailureLikeRust pins the Rust CLI contract
// (apply-patch scenario 015_failure_after_partial_success_leaves_changes):
// hunks are applied sequentially, so a failure after an earlier success leaves
// the earlier changes on disk. The verify-first flow is only used by the
// app-server tool (see TestApplyVerifiesBeforeApplyingLikeRustTool).
func TestRunCLILeavesPartialSuccessOnFailureLikeRust(t *testing.T) {
	dir := t.TempDir()
	patch := "*** Begin Patch\n*** Add File: created.txt\n+hello\n*** Update File: missing.txt\n@@\n-old\n+new\n*** End Patch"
	var stdout, stderr bytes.Buffer
	code := RunCLI([]string{patch}, strings.NewReader(""), &stdout, &stderr, dir)
	if code == 0 {
		t.Fatalf("RunCLI code = 0, want failure")
	}
	data, err := os.ReadFile(filepath.Join(dir, "created.txt"))
	if err != nil {
		t.Fatalf("created.txt missing after partial failure: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("created.txt = %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.txt")); !os.IsNotExist(err) {
		t.Fatalf("missing.txt should not exist, stat err = %v", err)
	}
}
