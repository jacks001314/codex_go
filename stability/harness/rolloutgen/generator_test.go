package rolloutgen

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateRespectsSizeAndShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	const total = 256 << 10
	stats, err := Generate(Spec{Path: path, TotalBytes: total, MaxLineSize: 8 << 10, Seed: 7})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if stats.Bytes < total {
		t.Fatalf("bytes = %d, want at least %d", stats.Bytes, total)
	}
	if stats.Bytes > total+(8<<10) {
		t.Fatalf("bytes = %d, want at most %d", stats.Bytes, total+(8<<10))
	}
	if stats.Turns == 0 || stats.Lines == 0 {
		t.Fatalf("stats = %+v, want at least one turn and line", stats)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatal("fixture does not end with a newline, so the last record is incomplete")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64<<10), 64<<10)
	lines := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		lines++
		if len(line)+1 > 8<<10 {
			t.Fatalf("line %d is %d bytes, over the 8 KiB cap", lines, len(line)+1)
		}
		var decoded map[string]any
		if err := json.Unmarshal(line, &decoded); err != nil {
			t.Fatalf("line %d is not JSON: %v", lines, err)
		}
		if decoded["type"] == "" {
			t.Fatalf("line %d has no type: %s", lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if lines != stats.Lines {
		t.Fatalf("counted %d lines, stats say %d", lines, stats.Lines)
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.jsonl")
	second := filepath.Join(dir, "b.jsonl")
	spec := Spec{TotalBytes: 128 << 10, MaxLineSize: 4 << 10, Seed: 42}
	if _, err := Generate(Spec{Path: first, TotalBytes: spec.TotalBytes, MaxLineSize: spec.MaxLineSize, Seed: spec.Seed}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := Generate(Spec{Path: second, TotalBytes: spec.TotalBytes, MaxLineSize: spec.MaxLineSize, Seed: spec.Seed}); err != nil {
		t.Fatalf("second: %v", err)
	}
	left, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	right, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("same seed produced different fixtures")
	}
}

func TestGenerateRejectsBadSpec(t *testing.T) {
	if _, err := Generate(Spec{Path: filepath.Join(t.TempDir(), "x.jsonl")}); err == nil {
		t.Fatal("zero TotalBytes was accepted")
	}
	if _, err := Generate(Spec{Path: filepath.Join(t.TempDir(), "x.jsonl"), TotalBytes: 1, MaxLineSize: 64}); err == nil {
		t.Fatal("tiny MaxLineSize was accepted")
	}
	if _, err := Generate(Spec{TotalBytes: 1}); err == nil {
		t.Fatal("empty Path was accepted")
	}
}

func TestGeneratedLinesSurviveReverseScan(t *testing.T) {
	// The first record must be session metadata, because the memory reader
	// validates the header before walking the tail.
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	if _, err := Generate(Spec{Path: path, TotalBytes: 32 << 10, MaxLineSize: 4 << 10}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	first, _, _ := bytes.Cut(data, []byte("\n"))
	var header struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(first, &header); err != nil {
		t.Fatalf("header is not JSON: %v", err)
	}
	if header.Type != "session_meta" {
		t.Fatalf("first record type = %q, want session_meta", header.Type)
	}
	if len(header.Payload) == 0 || header.Payload[0] != '{' {
		t.Fatalf("header payload is not an object: %s", header.Payload)
	}
}
