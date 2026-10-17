package memories

// Bounded rollout input for memory extraction (Rust #53940).
//
// Rust parity: codex-rs/memories/write/src/rollout_input.rs
// `read_recent_rollout_items` (rollout_input.rs:35-87) with MAX_ROLLOUT_BYTES /
// MAX_RECORD_BYTES (rollout_input.rs:28-29), backed by the reverse scanner in
// codex-rs/rollout/src/reverse_jsonl_scanner.rs:22-161 and the seekable reader
// in codex-rs/rollout/src/seekable_reader.rs:74-103. Extraction therefore sees
// the newest 8 MiB of sanitized evidence instead of the whole rollout, and a
// malformed or oversized record can no longer fail the job.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"codex_go/rollout"
)

const (
	// memoryRolloutMaxBytes is Rust's MAX_ROLLOUT_BYTES (rollout_input.rs:28).
	memoryRolloutMaxBytes = 8 * 1024 * 1024
	// memoryRolloutMaxRecordBytes is Rust's MAX_RECORD_BYTES (rollout_input.rs:29).
	memoryRolloutMaxRecordBytes = 1024 * 1024
	// memoryRolloutReadChunkBytes is Rust's READ_CHUNK_SIZE
	// (reverse_jsonl_scanner.rs:11).
	memoryRolloutReadChunkBytes = 64 * 1024
	// memoryRolloutHeaderError is Rust's `anyhow::ensure!` message
	// (rollout_input.rs:50).
	memoryRolloutHeaderError = "memory extraction requires valid session metadata within the record limit"
	// memoryRolloutResolveAttempts and memoryRolloutResolveDelay mirror the
	// representation-transition retry in Rust's `RolloutReader::open`
	// (seekable_reader.rs:26-39).
	memoryRolloutResolveAttempts = 4
	memoryRolloutResolveDelay    = 50 * time.Millisecond
)

// readRecentRolloutItems mirrors Rust's `read_recent_rollout_items`
// (rollout_input.rs:35-87): validate the session header, walk the rollout from
// newest to oldest keeping sanitized response items and inter-agent
// communications until the byte budget is exhausted, then restore chronological
// order.
//
// Structural difference: Rust runs the scan in `spawn_blocking` and abandons it
// when the caller drops the oneshot receiver (rollout_input.rs:37-38, :55). The
// Go port is synchronous, so a caller that stops caring keeps a bounded scan
// running instead of cancelling it mid-file; the scan never buffers more than
// the byte budget plus one 64 KiB chunk.
func readRecentRolloutItems(path string) ([]rollout.Line, error) {
	reader, err := openMemoryRolloutReader(path)
	if err != nil {
		return nil, err
	}
	defer reader.close()

	if err := reader.validateSessionHeader(); err != nil {
		return nil, err
	}

	scanner := newMemoryReverseJSONLScanner(reader, reader.size)
	remaining := memoryRolloutMaxBytes
	var items []rollout.Line
	for {
		raw, found, err := scanner.scanNext()
		if err != nil {
			return nil, err
		}
		if !found {
			break
		}
		line, ok := decodeMemoryRolloutLine(raw)
		if !ok {
			// Rust: `ScanOutcome::Rejected(_) => continue` (rollout_input.rs:59-61)
			// skips a malformed record instead of failing the extraction.
			continue
		}
		charged, ok := sanitizeMemoryRolloutLine(&line)
		if !ok {
			continue
		}
		if charged > remaining {
			// Rust: `checked_sub(...) => break` (rollout_input.rs:74-77) ends the
			// scan at the first record that no longer fits the budget.
			break
		}
		remaining -= charged
		items = append(items, line)
	}
	// Rust: `items.reverse()` (rollout_input.rs:81) restores source order.
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	return items, nil
}

// sanitizeMemoryRolloutLine applies Rust's record filter and byte charge
// (rollout_input.rs:62-79). It reports false when the record carries no memory
// evidence, otherwise the number of bytes charged against the budget.
func sanitizeMemoryRolloutLine(line *rollout.Line) (int, bool) {
	switch line.Type {
	case "inter_agent_communication":
		if len(line.Payload) == 0 {
			return 0, false
		}
		return len(line.Payload), true
	case "item":
		if len(line.Item) == 0 {
			return 0, false
		}
		var item map[string]any
		if err := json.Unmarshal(line.Item, &item); err != nil {
			return 0, false
		}
		sanitized, ok := sanitizeMemoryResponseItem(item)
		if !ok {
			return 0, false
		}
		encoded, err := json.Marshal(sanitized)
		if err != nil {
			return 0, false
		}
		// Rust charges `serde_json::to_vec(&line.item).len()` after
		// `sanitize_response_item_for_memories` (rollout_input.rs:68-72, :74), so the
		// budget counts retained evidence rather than dropped context blocks. The
		// Go charge is the sanitized record's JSON bytes, which is the same
		// quantity both serializers render.
		line.Item = encoded
		return len(encoded), true
	default:
		return 0, false
	}
}

// decodeMemoryRolloutLine decodes one persisted record into the Line shape the
// serializers consume, normalizing the `response_item` wire form exactly like
// rollout.unmarshalLine (rollout/rollout.go:1592-1640).
func decodeMemoryRolloutLine(raw []byte) (rollout.Line, bool) {
	var line rollout.Line
	if err := json.Unmarshal(raw, &line); err != nil {
		return rollout.Line{}, false
	}
	if line.Type == "response_item" {
		if len(line.Item) == 0 {
			line.Item = line.Payload
		}
		// Keep the legacy wire payload from retaining the unsanitized ciphertext
		// after the item has been normalized.
		line.Payload = nil
		line.Type = "item"
	}
	return line, true
}

// memoryRolloutReader exposes the rollout's logical JSONL bytes for random
// access, mirroring Rust's `open_rollout_seekable_reader`
// (seekable_reader.rs:74-103): plain rollouts are read in place, compressed
// rollouts are decoded into an anonymous temporary file so the scan stays
// seekable and bounded while the Codex home stays read-only. Rust can unlink the
// anonymous file immediately; Go keeps the name and removes it on close.
type memoryRolloutReader struct {
	*os.File
	size     int64
	tempPath string
}

func (r *memoryRolloutReader) close() {
	if r == nil || r.File == nil {
		return
	}
	_ = r.File.Close()
	if r.tempPath != "" {
		_ = os.Remove(r.tempPath)
	}
}

func openMemoryRolloutReader(path string) (*memoryRolloutReader, error) {
	existing, ok := resolveMemoryRolloutPath(path)
	if !ok {
		return nil, os.ErrNotExist
	}
	file, err := os.Open(existing)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		// Rust rejects nonregular rollouts before reading them
		// (seekable_reader.rs:49-51, :56-58).
		_ = file.Close()
		return nil, fmt.Errorf("rollout %s is not a regular file", existing)
	}
	if !strings.HasSuffix(strings.ToLower(existing), ".zst") {
		return &memoryRolloutReader{File: file, size: info.Size()}, nil
	}
	temp, err := os.CreateTemp("", "codex-memory-rollout-*.jsonl")
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	failed := func(cause error) (*memoryRolloutReader, error) {
		_ = temp.Close()
		_ = os.Remove(temp.Name())
		return nil, cause
	}
	decoder, err := zstd.NewReader(file)
	if err != nil {
		_ = file.Close()
		return failed(err)
	}
	_, copyErr := io.Copy(temp, decoder)
	decoder.Close()
	closeErr := file.Close()
	if copyErr != nil {
		return failed(copyErr)
	}
	if closeErr != nil {
		return failed(closeErr)
	}
	decoded, err := temp.Stat()
	if err != nil {
		return failed(err)
	}
	return &memoryRolloutReader{File: temp, size: decoded.Size(), tempPath: temp.Name()}, nil
}

// resolveMemoryRolloutPath mirrors Rust's representation resolution with its
// transition retry (seekable_reader.rs:26-39): the rollout may briefly be
// missing from both representations while a compression pass swaps them.
func resolveMemoryRolloutPath(path string) (string, bool) {
	for attempt := 0; attempt < memoryRolloutResolveAttempts; attempt++ {
		if existing, ok := rollout.ExistingRolloutPath(path); ok {
			return existing, true
		}
		if attempt+1 < memoryRolloutResolveAttempts {
			time.Sleep(memoryRolloutResolveDelay)
		}
	}
	return "", false
}

// validateSessionHeader mirrors Rust's header check (rollout_input.rs:40-51):
// the first record must parse and carry session metadata, read within the same
// per-record limit so an oversized instruction blob is never pulled in memory.
func (r *memoryRolloutReader) validateSessionHeader() error {
	head := make([]byte, memoryRolloutMaxRecordBytes)
	read, err := r.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	head = head[:read]
	if newline := bytes.IndexByte(head, '\n'); newline >= 0 {
		head = head[:newline]
	}
	var wire struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(head, &wire); err != nil {
		// Rust propagates the parse error from `parse_rollout_line`
		// (rollout_input.rs:47).
		return fmt.Errorf("memory extraction rollout header: %w", err)
	}
	if wire.Type != "session_meta" || !isJSONObject(wire.Payload) {
		return errors.New(memoryRolloutHeaderError)
	}
	// Rust's `RolloutItem::SessionMeta` decode validates the whole metadata
	// struct, including the strict `ThreadHistoryMode` enum
	// (codex-rs/protocol/src/protocol.rs:885-889) that rejects unknown modes.
	// Go's SessionMeta keeps history_mode a plain string, so the memory path
	// enforces the enum here.
	var meta struct {
		HistoryMode string `json:"history_mode"`
	}
	if err := json.Unmarshal(wire.Payload, &meta); err != nil {
		return errors.New(memoryRolloutHeaderError)
	}
	mode := strings.ToLower(strings.TrimSpace(meta.HistoryMode))
	if mode != "" && mode != "legacy" && mode != "paginated" {
		return errors.New(memoryRolloutHeaderError)
	}
	return nil
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// memoryReverseJSONLScanner ports Rust's `ReverseJsonlScanner`
// (codex-rs/rollout/src/reverse_jsonl_scanner.rs:22-161): records are read from
// the end of the file in 64 KiB chunks and records larger than maxRecordBytes
// are discarded without being buffered or parsed.
type memoryReverseJSONLScanner struct {
	reader              io.ReaderAt
	nextChunkEnd        int64
	chunkPosition       int
	chunk               []byte
	recordReversed      []byte
	maxRecordBytes      int
	discardingOversized bool
}

func newMemoryReverseJSONLScanner(reader io.ReaderAt, end int64) *memoryReverseJSONLScanner {
	return &memoryReverseJSONLScanner{
		reader:         reader,
		nextChunkEnd:   end,
		chunk:          make([]byte, memoryRolloutReadChunkBytes),
		maxRecordBytes: memoryRolloutMaxRecordBytes,
	}
}

// scanNext returns the next nonblank record from the end of the file. found is
// false once the scan reaches the beginning of the file.
func (s *memoryReverseJSONLScanner) scanNext() ([]byte, bool, error) {
	for {
		if s.chunkPosition == 0 {
			if s.nextChunkEnd == 0 {
				if s.discardingOversized {
					s.discardingOversized = false
					return nil, false, nil
				}
				record := s.finishRecord()
				if record == nil {
					return nil, false, nil
				}
				return record, true, nil
			}
			readSize := memoryRolloutReadChunkBytes
			if s.nextChunkEnd < int64(readSize) {
				readSize = int(s.nextChunkEnd)
			}
			s.nextChunkEnd -= int64(readSize)
			if _, err := s.reader.ReadAt(s.chunk[:readSize], s.nextChunkEnd); err != nil {
				return nil, false, err
			}
			s.chunkPosition = readSize
		}
		chunk := s.chunk[:s.chunkPosition]
		newline := bytes.LastIndexByte(chunk, '\n')
		if newline < 0 {
			// reverse_jsonl_scanner.rs:118-130: the chunk holds no record
			// boundary, so its bytes extend the record that continues earlier.
			if !s.discardingOversized {
				if len(s.recordReversed)+len(chunk) > s.maxRecordBytes {
					s.recordReversed = s.recordReversed[:0]
					s.discardingOversized = true
				} else {
					appendReversedBytes(&s.recordReversed, chunk)
				}
			}
			s.chunkPosition = 0
			continue
		}
		fragment := chunk[newline+1:]
		if !s.discardingOversized {
			if len(s.recordReversed)+len(fragment) > s.maxRecordBytes {
				s.recordReversed = s.recordReversed[:0]
				s.discardingOversized = true
			} else {
				appendReversedBytes(&s.recordReversed, fragment)
			}
		}
		s.chunkPosition = newline
		if s.discardingOversized {
			// reverse_jsonl_scanner.rs:111-114: the oversized record was skipped
			// whole, and the scan continues with the record before it.
			s.discardingOversized = false
			continue
		}
		if record := s.finishRecord(); record != nil {
			return record, true, nil
		}
	}
}

// finishRecord reverses the accumulated record and returns nil for a blank
// record, mirroring `ReverseJsonlScanner::finish_record`
// (reverse_jsonl_scanner.rs:145-160).
func (s *memoryReverseJSONLScanner) finishRecord() []byte {
	reverseBytes(s.recordReversed)
	if isBlankRecord(s.recordReversed) {
		s.recordReversed = s.recordReversed[:0]
		return nil
	}
	record := append([]byte(nil), s.recordReversed...)
	s.recordReversed = s.recordReversed[:0]
	return record
}

func appendReversedBytes(dst *[]byte, src []byte) {
	for index := len(src) - 1; index >= 0; index-- {
		*dst = append(*dst, src[index])
	}
}

func reverseBytes(value []byte) {
	for left, right := 0, len(value)-1; left < right; left, right = left+1, right-1 {
		value[left], value[right] = value[right], value[left]
	}
}

// isBlankRecord mirrors Rust's `iter().all(u8::is_ascii_whitespace)`, which is
// also true for an empty record.
func isBlankRecord(record []byte) bool {
	for _, value := range record {
		switch value {
		case ' ', '\t', '\n', '\v', '\f', '\r':
		default:
			return false
		}
	}
	return true
}
