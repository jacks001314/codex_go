package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestResumeMaterializesCompressedRolloutAndRefreshesMTimeLikeRust(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "rollout-2026-01-02T03-04-05-thread.jsonl")
	line, err := json.Marshal(Line{
		Type: "session_meta",
		Meta: &SessionMeta{ID: "thread", CWD: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compressed := plain + ".zst"
	if err := os.WriteFile(compressed, encoder.EncodeAll(append(line, '\n'), nil), 0o600); err != nil {
		t.Fatal(err)
	}
	encoder.Close()
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(compressed, old, old); err != nil {
		t.Fatal(err)
	}
	recorded, err := Resume(compressed)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	defer recorded.Close()
	if recorded.Path() != plain {
		t.Fatalf("Resume() path = %q, want %q", recorded.Path(), plain)
	}
	if _, err := os.Stat(compressed); !os.IsNotExist(err) {
		t.Fatalf("compressed rollout still exists: %v", err)
	}
	info, err := os.Stat(plain)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Fatalf("restored rollout mtime = %v, want a fresh timestamp", info.ModTime())
	}
}
