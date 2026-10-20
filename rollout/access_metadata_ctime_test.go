//go:build linux || darwin

package rollout

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSameRolloutStateDetectsCTimeOnlyChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	state, err := coldRolloutState(path)
	if err != nil || state == nil || !state.hasCTime {
		t.Fatalf("coldRolloutState() = %#v, %v", state, err)
	}
	// Keep mtime and mode unchanged; only ctime moves.
	if err := os.Chtimes(path, time.Now(), old); err != nil {
		t.Fatal(err)
	}
	same, err := sameRolloutState(path, state)
	if err != nil {
		t.Fatal(err)
	}
	if same {
		t.Fatal("sameRolloutState accepted a ctime-only access metadata change")
	}
}
