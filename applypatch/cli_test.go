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
