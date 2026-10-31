package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyRequiresTaskIDLikeRust pins that `codex apply` needs its positional
// argument: Rust's clap parser rejects the bare command before any stdin read,
// so the historical Go stdin-patch path is gone.
func TestApplyRequiresTaskIDLikeRust(t *testing.T) {
	dir := t.TempDir()
	patch := `*** Begin Patch
*** Add File: created.txt
+hello
*** End Patch`
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"-C", dir, "apply"}, strings.NewReader(patch), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("apply error = %v, want clap diagnostic with exit status 2", err)
	}
	diagnostic := stderr.String()
	if !strings.Contains(diagnostic, "error: the following required arguments were not provided:\n  <TASK_ID>") ||
		!strings.Contains(diagnostic, "Usage: codex apply <TASK_ID>") {
		t.Fatalf("apply stderr = %q", diagnostic)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if _, statErr := os.Stat(filepath.Join(dir, "created.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("apply created created.txt from stdin: %v", statErr)
	}
}

func TestApplyPatchFromArgument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	patch := `*** Begin Patch
*** Update File: target.txt
@@
-old
+new
*** End Patch`
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"-C", dir, "apply", patch}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("apply returned error: %v", err)
	}
	if got := readAppApplyFile(t, path); got != "new\n" {
		t.Fatalf("target = %q", got)
	}
}

func readAppApplyFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	return string(data)
}
