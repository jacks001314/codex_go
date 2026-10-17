package memories

// Rust parity: codex-rs/memories/write/src/rollout_input.rs (Rust #53940) with
// codex-rs/memories/write/src/rollout_input_tests.rs
// `recent_rollout_items_bound_history_and_preserve_newest_records`.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"codex_go/rollout"
)

const rolloutFixtureTimestamp = "2026-10-17T03:04:05Z"

// rolloutBoundGoldenV1 and rolloutBoundGoldenV2 were captured from
// SerializeFilteredRolloutForMemory and SerializeTieredRolloutForMemory for
// goldenRolloutFixture before the bounded reader landed (Rust #53940). A rollout
// that fits the byte budget must still serialize to exactly these bytes.
const (
	rolloutBoundGoldenV1 = `[{"content":[{"text":"\u003cenvironment_context\u003e\n\u003ccwd\u003e/tmp\u003c/cwd\u003e\n\u003c/environment_context\u003e","type":"input_text"},{"text":"remember this","type":"input_text"}],"role":"user","type":"message"},{"content":[{"text":"assistant response","type":"output_text"}],"role":"assistant","type":"message"},{"arguments":"{}","call_id":"call-1","name":"tool-name","type":"function_call"},{"content":[{"image_url":"data:image/png;base64,SECRETIMAGE","type":"input_image"}],"role":"user","type":"message"},{"from":"agent-a","payload":"inter agent note","to":"agent-b"}]`
	rolloutBoundGoldenV2 = `[harness context]
{"content":[{"text":"\u003cenvironment_context\u003e\n\u003ccwd\u003e/tmp\u003c/cwd\u003e\n\u003c/environment_context\u003e","type":"input_text"},{"text":"remember this","type":"input_text"}],"role":"user","type":"message"}
[assistant final]
{"content":[{"text":"assistant response","type":"output_text"}],"role":"assistant","type":"message"}
[tool]
{"arguments":"{}","call_id":"call-1","name":"tool-name","type":"function_call"}
[human user]
{"content":[{"text":"[image omitted]","type":"input_text"}],"role":"user","type":"message"}
[other agent]
{"from":"agent-a","payload":"inter agent note","to":"agent-b"}
`
)

// rolloutSessionMetaLine renders the session_meta record a recorder writes, so
// fixtures carry a production-shaped header.
func rolloutSessionMetaLine(t *testing.T, threadID, historyMode string) string {
	t.Helper()
	raw, err := json.Marshal(rollout.Line{
		Type:      "session_meta",
		Timestamp: rolloutFixtureTimestamp,
		Meta: &rollout.SessionMeta{
			ID:            threadID,
			SessionID:     threadID,
			Timestamp:     rolloutFixtureTimestamp,
			CWD:           "/workspace",
			Originator:    "cli",
			CLIVersion:    "0.0.0",
			ModelProvider: "openai",
			HistoryMode:   historyMode,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// rolloutItemLine renders one persisted response-item record.
func rolloutItemLine(t *testing.T, item map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(rollout.Line{Type: "response_item", Timestamp: rolloutFixtureTimestamp, Item: payload})
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

func rolloutMessageLine(t *testing.T, role, text string) string {
	t.Helper()
	return rolloutItemLine(t, map[string]any{
		"type":    "message",
		"role":    role,
		"content": []any{map[string]any{"type": "input_text", "text": text}},
	})
}

// writeBoundRollout writes already-rendered records as a rollout file.
func writeBoundRollout(t *testing.T, home, threadID string, records []string) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "10", "17")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-10-17T03-04-05-"+threadID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(records, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// goldenRolloutFixture writes the rich rollout the byte-identity golden was
// captured from.
func goldenRolloutFixture(t *testing.T, home, threadID string) string {
	t.Helper()
	now := time.Date(2026, 10, 17, 3, 4, 5, 0, time.UTC)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: threadID, SessionID: threadID, Source: "cli", ThreadSource: "user",
		CWD: "/workspace", Model: "gpt-test", ModelProvider: "openai", HistoryMode: "legacy",
		MemoryMode: "enabled", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	appendItem := func(value map[string]any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.AppendLine(rollout.Line{Type: "item", Timestamp: now.Format(time.RFC3339Nano), Item: raw}); err != nil {
			t.Fatal(err)
		}
	}
	appendItem(map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": "developer secret"}}})
	appendItem(map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "# AGENTS.md instructions for /tmp\n<INSTRUCTIONS>\nignore\n</INSTRUCTIONS>"},
		map[string]any{"type": "input_text", "text": "<skill>\nsecret\n</skill>"},
		map[string]any{"type": "input_text", "text": "<environment_context>\n<cwd>/tmp</cwd>\n</environment_context>"},
		map[string]any{"type": "input_text", "text": "remember this"},
	}})
	appendItem(map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "assistant response"}}})
	appendItem(map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": "reasoning secret"}}})
	appendItem(map[string]any{"type": "function_call", "name": "tool-name", "call_id": "call-1", "arguments": `{}`})
	appendItem(map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,SECRETIMAGE"}}})
	inter, err := json.Marshal(map[string]any{"from": "agent-a", "to": "agent-b", "payload": "inter agent note"})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendLine(rollout.Line{Type: "inter_agent_communication", Timestamp: now.Format(time.RFC3339Nano), Payload: inter}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	return recorder.Path()
}

// Mirrors Rust's `recent_rollout_items_bound_history_and_preserve_newest_records`:
// a rollout far larger than the budget yields only its newest evidence, in
// source order, without loading the whole file.
func TestReadRecentRolloutItemsBoundsHistoryLikeRust(t *testing.T) {
	const recordCount = 12000
	home := t.TempDir()
	records := make([]string, 0, recordCount+1)
	records = append(records, rolloutSessionMetaLine(t, "bound-thread", "legacy"))
	filler := strings.Repeat("evidence ", 400)
	for index := 0; index < recordCount; index++ {
		records = append(records, rolloutMessageLine(t, "user", fmt.Sprintf("record %d %s", index, filler)))
	}
	path := writeBoundRollout(t, home, "bound-thread", records)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 2*memoryRolloutMaxBytes {
		t.Fatalf("fixture is %d bytes, want a rollout well over the %d byte budget", info.Size(), memoryRolloutMaxBytes)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	items, err := readRecentRolloutItems(path)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	// A whole-file load keeps a decoded copy of every record, so it allocates on
	// the order of twice the file; the bounded scan only decodes the evidence it
	// keeps, which is capped by the budget whatever the file size is.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= 2*uint64(info.Size()) {
		t.Fatalf("bounded read allocated %d bytes for a %d byte rollout", allocated, info.Size())
	}
	if len(items) == 0 {
		t.Fatal("bounded read retained no evidence")
	}
	if len(items) >= recordCount {
		t.Fatalf("bounded read retained all %d records", len(items))
	}
	charged := 0
	for _, item := range items {
		charged += len(item.Item)
	}
	if charged > memoryRolloutMaxBytes {
		t.Fatalf("retained %d bytes, want <= %d", charged, memoryRolloutMaxBytes)
	}
	// The retained window is the tail of the file, so the budget stays filled and
	// the newest record survives while the oldest is dropped.
	if charged < memoryRolloutMaxBytes-8*1024 {
		t.Fatalf("retained only %d bytes, want the budget filled to within one record", charged)
	}
	if !strings.Contains(string(items[len(items)-1].Item), fmt.Sprintf("record %d ", recordCount-1)) {
		t.Fatal("newest evidence was dropped")
	}
	if strings.Contains(string(items[0].Item), "record 0 ") {
		t.Fatal("oldest evidence survived the byte budget")
	}

	// The same bound reaches the serializer production calls.
	rendered, err := SerializeTieredRolloutForMemory(path, 200_000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, fmt.Sprintf("record %d ", recordCount-1)) {
		t.Fatal("extraction input is missing the newest record")
	}
	if strings.Contains(rendered, "record 0 ") {
		t.Fatal("extraction input reached past the byte budget")
	}
}

// Rust puts an oversized record and an invalid record between the newest
// evidence and still expects the newest valid records to survive
// (rollout_input_tests.rs:28-41).
func TestReadRecentRolloutItemsSkipsMalformedAndOversizedRecordsLikeRust(t *testing.T) {
	home := t.TempDir()
	records := []string{
		rolloutSessionMetaLine(t, "skip-thread", "legacy"),
		rolloutMessageLine(t, "user", "older evidence"),
		rolloutMessageLine(t, "user", "recent question"),
		strings.Repeat("x", memoryRolloutMaxRecordBytes+1),
		"\xff",
		"",
		rolloutMessageLine(t, "user", "latest correction"),
	}
	path := writeBoundRollout(t, home, "skip-thread", records)
	items, err := readRecentRolloutItems(path)
	if err != nil {
		t.Fatalf("malformed or oversized records failed the read: %v", err)
	}
	want := []string{"older evidence", "recent question", "latest correction"}
	if len(items) != len(want) {
		t.Fatalf("retained %d records, want %d", len(items), len(want))
	}
	for index, text := range want {
		if !strings.Contains(string(items[index].Item), text) {
			t.Fatalf("record %d = %s, want %q in source order", index, items[index].Item, text)
		}
	}
	rendered, err := SerializeTieredRolloutForMemory(path, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "latest correction") {
		t.Fatal("evidence after the oversized record never reached the extraction input")
	}
	if strings.Contains(rendered, "xxxxxx") {
		t.Fatal("the oversized record reached the extraction input")
	}
}

// Rust requires valid session metadata in the first record, read within the same
// record limit (rollout_input.rs:40-51; rollout_input_tests.rs:57-60).
func TestReadRecentRolloutItemsRequiresValidSessionHeaderLikeRust(t *testing.T) {
	evidence := rolloutMessageLine(t, "user", "evidence")
	cases := []struct {
		name    string
		records []string
		wantErr string
	}{
		{
			// ThreadHistoryMode only knows legacy and paginated, so an unknown
			// mode fails the whole extraction (protocol.rs:885-889).
			name:    "unsupported history mode",
			records: []string{rolloutSessionMetaLine(t, "future-thread", "future"), evidence},
			wantErr: memoryRolloutHeaderError,
		},
		{
			name:    "first record is not session metadata",
			records: []string{evidence},
			wantErr: memoryRolloutHeaderError,
		},
		{
			name:    "header is not json",
			records: []string{"{not json", evidence},
			wantErr: "memory extraction rollout header",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeBoundRollout(t, t.TempDir(), "header-thread", tc.records)
			_, err := readRecentRolloutItems(path)
			if err == nil {
				t.Fatal("invalid session metadata was accepted")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
	if _, err := readRecentRolloutItems(filepath.Join(t.TempDir(), "missing.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing rollout error = %v, want os.ErrNotExist", err)
	}
}

// A rollout inside the byte budget must serialize byte-for-byte as it did before
// the bound landed, for both memory versions.
func TestBoundedRolloutInputKeepsSmallRolloutsByteIdentical(t *testing.T) {
	home := t.TempDir()
	path := goldenRolloutFixture(t, home, "golden-thread")
	v1, err := SerializeFilteredRolloutForMemory(path)
	if err != nil {
		t.Fatal(err)
	}
	if v1 != rolloutBoundGoldenV1 {
		t.Fatalf("v1 extraction input changed:\n got %q\nwant %q", v1, rolloutBoundGoldenV1)
	}
	v2, err := SerializeTieredRolloutForMemory(path, 10_000)
	if err != nil {
		t.Fatal(err)
	}
	if v2 != rolloutBoundGoldenV2 {
		t.Fatalf("v2 extraction input changed:\n got %q\nwant %q", v2, rolloutBoundGoldenV2)
	}
	items, err := readRecentRolloutItems(path)
	if err != nil {
		t.Fatal(err)
	}
	// The developer message, the reasoning record and the excluded context blocks
	// are dropped; the remaining five records are kept in source order.
	if len(items) != 5 {
		t.Fatalf("retained %d records, want 5", len(items))
	}
	if items[0].Type != "item" || items[len(items)-1].Type != "inter_agent_communication" {
		t.Fatalf("retained record types = %q..%q, want item..inter_agent_communication",
			items[0].Type, items[len(items)-1].Type)
	}
}

// Rust #53963: large assistant ciphertext is stripped before the reader budget
// is charged, so unreadable ciphertext cannot displace earlier readable evidence.
func TestBoundedRolloutInputDropsCiphertextWithoutDisplacingEvidence(t *testing.T) {
	threadID := "ciphertext-thread"
	records := []string{
		rolloutSessionMetaLine(t, threadID, "legacy"),
		rolloutMessageLine(t, "user", "earlier extraction evidence"),
	}
	for i := 0; i < 20; i++ {
		records = append(records, rolloutItemLine(t, map[string]any{
			"type":              "message",
			"role":              "assistant",
			"encrypted_content": strings.Repeat("a", 512*1024),
			"content": []any{
				map[string]any{"type": "output_text", "text": fmt.Sprintf("assistant evidence %d", i)},
			},
		}))
	}
	records = append(records, rolloutMessageLine(t, "user", "newest correction"))
	path := writeBoundRollout(t, t.TempDir(), threadID, records)

	items, err := readRecentRolloutItems(path)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	text := string(serialized)
	if strings.Contains(text, "encrypted_content") {
		t.Fatal("bounded rollout retained assistant ciphertext")
	}
	for _, evidence := range []string{"earlier extraction evidence", "assistant evidence 19", "newest correction"} {
		if !strings.Contains(text, evidence) {
			t.Fatalf("bounded rollout dropped %q: %s", evidence, text)
		}
	}
	if len(text) >= 2*1024*1024 {
		t.Fatalf("bounded rollout retained %d bytes of ciphertext-era evidence", len(text))
	}
}

// Compressed rollouts take the seekable-reader path (seekable_reader.rs:74-103):
// the bytes are decoded into an anonymous snapshot so the bound, the record limit
// and the header check all still apply.
func TestReadRecentRolloutItemsReadsCompressedRolloutsLikeRust(t *testing.T) {
	home := t.TempDir()
	plain := writeBoundRollout(t, home, "compressed-thread", []string{
		rolloutSessionMetaLine(t, "compressed-thread", "legacy"),
		rolloutMessageLine(t, "user", "compressed older evidence"),
		rolloutMessageLine(t, "user", "compressed newest evidence"),
	})
	data, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plain+".zst", compressed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}

	// The caller still names the canonical plain path; the reader resolves the
	// compressed representation exactly like rollout.Load does.
	items, err := readRecentRolloutItems(plain)
	if err != nil {
		t.Fatalf("compressed rollout read failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("retained %d records, want 2", len(items))
	}
	if !strings.Contains(string(items[0].Item), "compressed older evidence") ||
		!strings.Contains(string(items[1].Item), "compressed newest evidence") {
		t.Fatalf("compressed evidence = %s | %s", items[0].Item, items[1].Item)
	}
}
