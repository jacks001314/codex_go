package rollout

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/session"
)

// Rust parity: codex-rs/rollout/src/compression.rs at #44138. A cold rollout
// with an active thread writer must be skipped and left in place; once the
// writer releases ownership the next pass compresses it.
func TestRolloutCompressionSkipsActiveWriterLikeRust(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, SessionsSubdir)
	path := filepath.Join(root, "rollout-2025-01-01T00-00-00-busy.jsonl")
	writeTestRollout(t, path, "busy", "")
	cold := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, cold, cold); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	store := session.NewStore(root)
	writer, err := store.AcquireWriter(session.ThreadID("busy"))
	if err != nil {
		t.Fatalf("AcquireWriter: %v", err)
	}
	outcome, err := compressRolloutIfCold(path, "busy", RolloutCompressionTriggerRPC, store)
	if err != nil {
		t.Fatalf("compressRolloutIfCold() error = %v", err)
	}
	if outcome != compressionSkippedBusy {
		t.Fatalf("outcome = %q, want %q", outcome, compressionSkippedBusy)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("busy rollout was removed: %v", err)
	}
	if _, err := os.Stat(path + ".zst"); !os.IsNotExist(err) {
		t.Fatalf("busy rollout was compressed (err=%v)", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}
	outcome, err = compressRolloutIfCold(path, "busy", RolloutCompressionTriggerRPC, store)
	if err != nil {
		t.Fatalf("compressRolloutIfCold() after release error = %v", err)
	}
	if outcome != compressionCompressed {
		t.Fatalf("outcome after release = %q, want %q", outcome, compressionCompressed)
	}
	if _, err := os.Stat(path + ".zst"); err != nil {
		t.Fatalf("compressed rollout missing: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("plain rollout still present after compression (err=%v)", err)
	}
}

// Rust parity: codex-rs/rollout/src/compression.rs at #44138. Publication probes
// an idle thread without creating its lock file, and an existing but unlocked
// lock file is still idle.
func TestRolloutCompressionPublicationLeavesNoWriterLockLikeRust(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, SessionsSubdir)
	store := session.NewStore(root)
	cold := time.Now().Add(-8 * 24 * time.Hour)

	idle := filepath.Join(root, "rollout-2025-01-01T00-00-00-idle.jsonl")
	writeTestRollout(t, idle, "idle", "")
	if err := os.Chtimes(idle, cold, cold); err != nil {
		t.Fatalf("Chtimes(idle): %v", err)
	}
	outcome, err := compressRolloutIfCold(idle, "idle", RolloutCompressionTriggerRPC, store)
	if err != nil {
		t.Fatalf("compressRolloutIfCold(idle) error = %v", err)
	}
	if outcome != compressionCompressed {
		t.Fatalf("idle outcome = %q, want %q", outcome, compressionCompressed)
	}
	lockPath := filepath.Join(root, "thread-writer-locks", "idle.lock")
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("publication created a thread lock file: %v", err)
	}

	stale := filepath.Join(root, "rollout-2025-01-01T00-00-00-stale.jsonl")
	writeTestRollout(t, stale, "stale", "")
	if err := os.Chtimes(stale, cold, cold); err != nil {
		t.Fatalf("Chtimes(stale): %v", err)
	}
	staleLock := filepath.Join(root, "thread-writer-locks", "stale.lock")
	if err := os.MkdirAll(filepath.Dir(staleLock), 0o700); err != nil {
		t.Fatalf("MkdirAll(lock dir): %v", err)
	}
	if err := os.WriteFile(staleLock, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(stale lock): %v", err)
	}
	outcome, err = compressRolloutIfCold(stale, "stale", RolloutCompressionTriggerRPC, store)
	if err != nil {
		t.Fatalf("compressRolloutIfCold(stale) error = %v", err)
	}
	if outcome != compressionCompressed {
		t.Fatalf("stale outcome = %q, want %q", outcome, compressionCompressed)
	}
}
