package rollout

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

// Mirrors Rust #51499's full-history regression
// (rollout/src/compression_tests.rs
// `full_history_load_preserves_records_and_errors_across_representations`):
// loading a rollout's history must yield the same records and the same
// parse-error count for the plain `.jsonl` and the compressed `.jsonl.zst`
// representation, including Unicode payloads, CRLF line endings, blank lines
// and malformed records.
//
// Go performs the whole read/parse pass synchronously on the caller's
// goroutine, so Rust's "one blocking worker" split is structural; the
// observable equivalence asserted here is the property the Rust change added
// tests for.
func TestLoadMatchesAcrossPlainAndCompressedRepresentations(t *testing.T) {
	home := t.TempDir()
	plain := writeRollout(t, home, "thread-representations", time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC), "message with unicode: π")
	original, err := os.ReadFile(plain)
	if err != nil {
		t.Fatalf("read rollout: %v", err)
	}
	baseline, baselineErrors, err := Load(plain)
	if err != nil || baselineErrors != 0 || len(baseline) != 2 {
		t.Fatalf("baseline load = lines=%#v errors=%d err=%v", baseline, baselineErrors, err)
	}

	// A whitespace-only separator line must be skipped (Rust `line.trim().is_empty()`),
	// the malformed record must be counted, and CRLF endings must not change results.
	body := strings.ReplaceAll(strings.TrimRight(string(original), "\r\n"), "\n", "\r\n")
	if err := os.WriteFile(plain, []byte(" \r\ninvalid JSON\r\n"+body+"\r\n"), 0o600); err != nil {
		t.Fatalf("rewrite rollout: %v", err)
	}
	plainLines, plainErrors, err := Load(plain)
	if err != nil {
		t.Fatalf("plain load: %v", err)
	}
	if plainErrors != 1 {
		t.Fatalf("plain parse errors = %d, want 1", plainErrors)
	}
	if !reflect.DeepEqual(plainLines, baseline) {
		t.Fatalf("plain load = %#v, want %#v", plainLines, baseline)
	}
	foundUnicode := false
	for _, line := range plainLines {
		if strings.Contains(string(line.Item), "π") {
			foundUnicode = true
		}
	}
	if !foundUnicode {
		t.Fatalf("unicode payload lost: %#v", plainLines)
	}

	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	compressed := plain + ".zst"
	if err := os.WriteFile(compressed, encoder.EncodeAll([]byte(" \r\ninvalid JSON\r\n"+body+"\r\n"), nil), 0o600); err != nil {
		t.Fatalf("write compressed rollout: %v", err)
	}
	encoder.Close()
	if err := os.Remove(plain); err != nil {
		t.Fatalf("remove plain rollout: %v", err)
	}

	compressedLines, compressedErrors, err := Load(plain)
	if err != nil {
		t.Fatalf("compressed load: %v", err)
	}
	if compressedErrors != plainErrors {
		t.Fatalf("compressed parse errors = %d, want %d", compressedErrors, plainErrors)
	}
	if !reflect.DeepEqual(compressedLines, plainLines) {
		t.Fatalf("compressed load = %#v, want %#v", compressedLines, plainLines)
	}
}
