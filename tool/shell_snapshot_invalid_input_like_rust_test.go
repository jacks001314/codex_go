package tool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

// Rust #47611 rejects a snapshot script containing a NUL byte before the shell
// runs. Go's os/exec refuses such arguments when the child starts, so the
// capture fails with EINVAL and never creates the file the script would write.
func TestSnapshotCaptureRejectsNULScriptBeforeExecutionLikeRust(t *testing.T) {
	dir := t.TempDir()
	command := []string{"sh", "-c", "printf ran > ran\x00"}
	if runtime.GOOS == "windows" {
		command = []string{"cmd.exe", "/C", "echo ran> ran\x00"}
	}
	_, err := runSnapshotCommandDirect(context.Background(), command, dir, nil)
	if err == nil {
		t.Fatal("NUL snapshot script was accepted")
	}
	if !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("NUL snapshot script error = %v, want EINVAL", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ran")); statErr == nil {
		t.Fatal("the shell ran despite the invalid NUL argument")
	}
}
