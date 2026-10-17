package rollout

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// Mirrors Rust `read_rollout_prefix` (codex-rs/rollout/src/seekable_reader.rs,
// #44175): decoded bytes are returned for either representation without
// materializing a durable JSONL file, and the bound applies after decoding.
func TestReadRolloutPrefixDecodesCompressedLikeRust(t *testing.T) {
	home := t.TempDir()
	plain := filepath.Join(home, "rollout-2026-09-09T12-00-00-0192f0aa-0000-7000-8000-0000000000b0.jsonl")
	payload := []byte("{\"a\":1}\n{\"b\":2}\n")
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd.NewWriter() error = %v", err)
	}
	defer encoder.Close()
	if err := os.WriteFile(plain+".zst", encoder.EncodeAll(payload, nil), 0o600); err != nil {
		t.Fatalf("WriteFile(compressed) error = %v", err)
	}

	got, ok, err := ReadRolloutPrefix(plain, int64(len(payload)))
	if err != nil || !ok {
		t.Fatalf("ReadRolloutPrefix: ok=%v err=%v", ok, err)
	}
	if string(got) != string(payload) {
		t.Fatalf("bytes = %q, want %q", string(got), string(payload))
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatalf("reading must not materialize the plain rollout, stat err = %v", err)
	}

	got, ok, err = ReadRolloutPrefix(plain, int64(len(payload))-1)
	if err != nil || !ok {
		t.Fatalf("bounded read: ok=%v err=%v", ok, err)
	}
	if len(got) != len(payload)-1 {
		t.Fatalf("bounded read = %d bytes, want %d", len(got), len(payload)-1)
	}

	current := []byte("{\"a\":3}\n")
	if err := os.WriteFile(plain, current, 0o600); err != nil {
		t.Fatalf("WriteFile(plain) error = %v", err)
	}
	got, ok, err = ReadRolloutPrefix(plain, 1024)
	if err != nil || !ok || string(got) != string(current) {
		t.Fatalf("plain sibling not preferred: ok=%v err=%v bytes=%q", ok, err, string(got))
	}

	if _, ok, err := ReadRolloutPrefix(filepath.Join(home, "missing.jsonl"), 16); err != nil || ok {
		t.Fatalf("missing path: ok=%v err=%v", ok, err)
	}
	if _, ok, err := ReadRolloutPrefix(home, 16); err != nil || ok {
		t.Fatalf("directory: ok=%v err=%v", ok, err)
	}
}
