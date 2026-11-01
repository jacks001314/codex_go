package execserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"codex_go/shell"
)

// Rust's CapturePurpose::Prewarm leaves the cache cell uninitialized when the
// speculative capture fails, so the first real launch captures immediately
// instead of inheriting the retry backoff.
func TestShellSnapshotPrewarmFailureDoesNotBackOffNextLaunchLikeRust(t *testing.T) {
	previous := shellSnapshotPlatformSupported
	shellSnapshotPlatformSupported = true
	t.Cleanup(func() { shellSnapshotPlatformSupported = previous })
	var calls atomic.Int64
	manager := newShellSnapshotManager()
	manager.capture = func(context.Context, []string, string, map[string]string) ([]byte, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("prewarm failed")
		}
		return []byte("# Snapshot file\nexport KEPT='kept'\n\x00\x00\x00"), nil
	}
	params := protectedSnapshotParams(t)
	if _, err := manager.prewarmSnapshotEntry(context.Background(), params, shell.ShellBash, params.Env, nil, nil); err == nil {
		t.Fatal("prewarm failure was not reported")
	}
	entry, err := manager.snapshotEntry(context.Background(), params, shell.ShellBash, params.Env, nil, nil)
	if err != nil {
		t.Fatalf("the first launch inherited the prewarm failure: %v", err)
	}
	if entry == nil || entry.state == "" {
		t.Fatalf("launch entry = %#v", entry)
	}
	if calls.Load() != 2 {
		t.Fatalf("captures = %d, want the prewarm retried immediately", calls.Load())
	}
	manager.close()
}
