package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPatchFromStdin(t *testing.T) {
	dir := t.TempDir()
	patch := `*** Begin Patch
*** Add File: created.txt
+hello
*** End Patch`
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"-C", dir, "apply"}, strings.NewReader(patch), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("apply returned error: %v", err)
	}
	if stdout.String() != "Success. Updated the following files:\nA created.txt\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "created.txt"))
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("created = %q", string(data))
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
