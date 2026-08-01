package rollout

import (
	"bytes"
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

func TestCompressedProjectionUsesJSONLByteOffsetsAndReferenceMaterializesPlain(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "rollout-2026-07-21T10-00-00-thread.jsonl")
	data := []byte(projectionLineJSON(0, "turn-0") + projectionLineJSON(1, "turn-1"))
	compressed := plain + ".zst"
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(compressed, encoder.EncodeAll(data, nil), 0o640); err != nil {
		t.Fatal(err)
	}
	encoder.Close()
	wantModified := fixedProjectionTime().AddDate(1, 0, 0)
	if err := os.Chtimes(compressed, wantModified, wantModified); err != nil {
		t.Fatal(err)
	}

	steps, nextOffset, err := ReadProjectionSteps(plain, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[1].EndByteOffset != uint64(len(data)) || nextOffset != uint64(len(data)) {
		t.Fatalf("compressed projection = steps %#v offset %d", steps, nextOffset)
	}
	if size, err := RolloutByteLength(compressed); err != nil || size != uint64(len(data)) {
		t.Fatalf("uncompressed byte length = %d, %v", size, err)
	}

	materialized, err := MaterializeRolloutForReference(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if materialized != plain {
		t.Fatalf("materialized path = %q, want %q", materialized, plain)
	}
	got, err := os.ReadFile(plain)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("materialized contents = %q, %v", got, err)
	}
	if _, err := os.Stat(compressed); !os.IsNotExist(err) {
		t.Fatalf("compressed sibling still exists: %v", err)
	}
	info, err := os.Stat(plain)
	if err != nil || !info.ModTime().Equal(wantModified) {
		t.Fatalf("materialized modified time = %v, %v", info.ModTime(), err)
	}
}
