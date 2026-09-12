package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTaskToolCapabilityMarkersLikeRust covers Rust's
// tui-thread-reference-capabilities marker directory: remembering a thread is a
// per-thread empty file, availability survives a new process, and malformed
// inputs are ignored.
func TestTaskToolCapabilityMarkersLikeRust(t *testing.T) {
	home := t.TempDir()
	if remoteTaskToolThreadAvailable(home, "thread-1") {
		t.Fatal("thread should not be available before it is remembered")
	}
	rememberRemoteTaskToolThread(home, "thread-1")
	if !remoteTaskToolThreadAvailable(home, "thread-1") {
		t.Fatal("remembered thread should be available")
	}
	if remoteTaskToolThreadAvailable(home, "thread-2") {
		t.Fatal("unrelated thread should not be available")
	}
	marker := filepath.Join(home, taskToolCapabilitiesDirName, "thread-1")
	info, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	if info.IsDir() || info.Size() != 0 {
		t.Fatalf("marker should be an empty file: dir=%v size=%d", info.IsDir(), info.Size())
	}
	// A path-like thread id stays inside the capability directory.
	rememberRemoteTaskToolThread(home, "a/b\\c")
	entries, err := os.ReadDir(filepath.Join(home, taskToolCapabilitiesDirName))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "thread-1" && entry.Name() != "a_b_c" {
			t.Fatalf("unexpected marker %q", entry.Name())
		}
	}
	// Empty inputs are no-ops.
	rememberRemoteTaskToolThread("", "thread-1")
	rememberRemoteTaskToolThread(home, " ")
	rememberRemoteTaskToolThread(home, "..")
	if remoteTaskToolThreadAvailable(home, " ") || remoteTaskToolThreadAvailable(home, "..") {
		t.Fatal("malformed thread ids should not create markers")
	}
	if remoteTaskToolThreadAvailable("", "thread-1") {
		t.Fatal("empty codex home should not report availability")
	}
}
