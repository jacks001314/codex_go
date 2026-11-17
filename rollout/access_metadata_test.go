package rollout

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSameRolloutStateDetectsAccessMetadataChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	state, cold, err := coldRolloutState(path)
	if err != nil || state == nil || !cold {
		t.Fatalf("coldRolloutState() = %#v, %v, %v", state, cold, err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	same, err := sameRolloutState(path, state)
	if err != nil {
		t.Fatal(err)
	}
	if same {
		t.Fatal("sameRolloutState accepted changed access metadata")
	}
}
