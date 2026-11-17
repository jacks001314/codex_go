//go:build linux

package rollout

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"codex_go/session"
)

// Rust parity: codex-rs/rollout/src/compression.rs at #62092. Scanning must not
// open a recent rollout's header, so a relatime mount cannot advance its access
// time.
func TestRolloutCompressionLeavesRecentAccessTimeLikeRust(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, SessionsSubdir)
	path := filepath.Join(root, "rollout-2025-01-01T00-00-00-fresh.jsonl")
	writeTestRollout(t, path, "fresh", "")
	// Reading this recently modified file would advance its old access time.
	if err := os.Chtimes(path, time.Unix(0, 0), time.Now()); err != nil {
		t.Fatalf("Chtimes(fresh): %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(fresh): %v", err)
	}
	wantAccessed := before.Sys().(*syscall.Stat_t).Atim

	stats := RolloutCompressionStats{}
	store := session.NewStore(root)
	if err := compressRolloutsInRoot(context.Background(), root, time.Now().Add(time.Minute), &stats, RolloutCompressionTriggerRPC, store); err != nil {
		t.Fatalf("compressRolloutsInRoot() error = %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(fresh): %v", err)
	}
	if gotAccessed := after.Sys().(*syscall.Stat_t).Atim; gotAccessed != wantAccessed {
		t.Fatalf("recent rollout access time = %v, want %v", gotAccessed, wantAccessed)
	}
}
