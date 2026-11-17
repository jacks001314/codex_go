package rollout

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/session"
)

// Rust parity: codex-rs/rollout/src/compression.rs at #62092. A rollout that is
// too recent to compress is skipped before its header is read, and the skip is
// reported through the same counters as a measured outcome.
func TestRolloutCompressionSkipsRecentBeforeReadingHeaderLikeRust(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, SessionsSubdir)
	freshPath := filepath.Join(root, "rollout-2025-01-01T00-00-00-fresh.jsonl")
	writeTestRollout(t, freshPath, "fresh", "")

	stats := RolloutCompressionStats{}
	store := session.NewStore(root)
	if err := compressRolloutsInRoot(context.Background(), root, time.Now().Add(time.Minute), &stats, RolloutCompressionTriggerRPC, store); err != nil {
		t.Fatalf("compressRolloutsInRoot() error = %v", err)
	}
	if stats.Scanned != 1 || stats.Skipped != 1 || stats.Compressed != 0 || stats.Failed != 0 {
		t.Fatalf("stats = %+v, want one scanned and skipped recent rollout", stats)
	}
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("recent rollout was removed: %v", err)
	}
	if _, err := os.Stat(freshPath + ".zst"); !os.IsNotExist(err) {
		t.Fatalf("recent rollout was compressed (err=%v)", err)
	}
}

// Rust parity: codex-rs/rollout/src/compression.rs at #62092. The malformed
// fixture is aged so it still reaches the header path and records a metadata
// read failure.
func TestRolloutCompressionAgedMalformedHeaderRecordsMetadataFailureLikeRust(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, SessionsSubdir)
	badPath := filepath.Join(root, "rollout-2025-01-03T12-00-01-bad.jsonl")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(badPath, []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(badPath, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	stats := RolloutCompressionStats{}
	store := session.NewStore(root)
	if err := compressRolloutsInRoot(context.Background(), root, time.Now().Add(time.Minute), &stats, RolloutCompressionTriggerRPC, store); err != nil {
		t.Fatalf("compressRolloutsInRoot() error = %v", err)
	}
	if !stats.ScanErrors {
		t.Fatalf("stats = %+v, want a scan error", stats)
	}
	if stats.MetadataReadFailures != 1 || stats.Skipped != 1 || stats.Scanned != 0 {
		t.Fatalf("stats = %+v, want one unreadable-metadata skip", stats)
	}
}
