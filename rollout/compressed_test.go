package rollout

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestLoadCompressedRolloutAndCanonicalPath(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "rollout-2026-07-21T10-00-00-thread.jsonl")
	line := Line{Type: "session_meta", Meta: &SessionMeta{ID: "thread", CWD: dir}}
	data, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	compressed := plain + ".zst"
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(compressed, encoder.EncodeAll(append(data, '\n'), nil), 0o600); err != nil {
		t.Fatal(err)
	}
	encoder.Close()
	loaded, parseErrors, err := Load(compressed)
	if err != nil || parseErrors != 0 || len(loaded) != 1 || loaded[0].Meta == nil || loaded[0].Meta.ID != "thread" {
		t.Fatalf("Load compressed = lines=%#v parseErrors=%d err=%v", loaded, parseErrors, err)
	}
	if got := PlainRolloutPath(compressed); got != plain {
		t.Fatalf("PlainRolloutPath = %q, want %q", got, plain)
	}
}
