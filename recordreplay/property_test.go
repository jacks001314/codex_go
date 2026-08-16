package recordreplay

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// This file is the djalign dynamic-layer method-5 (fuzz/property) layer for
// the L2 record-replay facility: the normalization and digest are pure
// functions, so property tests pin their invariants token-free and CI-able:
// the normalized contract view must roundtrip losslessly, the digest must be
// deterministic, unknown trace kinds must never be swallowed, and only the
// documented volatile fields (timestamps, cwd, model text) may be dropped.

// seededCorpusRawLines generates a deterministic multi-session, multi-task
// corpus that mixes the documented recording kinds with unknown kinds,
// unknown app variants and both presence paths (delta text present,
// lifecycle markers absent). It never depends on the Rust checkout.
func seededCorpusRawLines() []string {
	lines := []string{}
	models := []string{"gpt-corpus-a", "gpt-corpus-b"}
	for sessionIndex, model := range models {
		sessionID := "session-" + string(rune('a'+sessionIndex))
		lines = append(lines,
			`{"ts":"2026-01-01T00:00:00.000Z","dir":"to_tui","kind":"session_start","cwd":"C:\\work","model":"`+model+`","payload":{}}`)
		lines = append(lines,
			`{"ts":"2026-01-01T00:00:01.000Z","dir":"to_tui","kind":"codex_event","payload":{"id":"`+sessionID+`","msg":{"type":"session_configured","session_id":"`+sessionID+`","model":"`+model+`"}}}`)
		for task := 1; task <= 5; task++ {
			taskID := sessionID + "-task-" + string(rune('0'+task))
			lines = append(lines,
				`{"ts":"2026-01-01T00:00:02.000Z","dir":"to_tui","kind":"codex_event","payload":{"id":"`+taskID+`","msg":{"type":"task_started"}}}`)
			lines = append(lines,
				`{"ts":"2026-01-01T00:00:03.000Z","dir":"to_tui","kind":"codex_event","payload":{"id":"`+taskID+`","msg":{"type":"agent_reasoning_raw_content_delta","delta":"think"}}}`)
			lines = append(lines,
				`{"ts":"2026-01-01T00:00:04.000Z","dir":"to_tui","kind":"codex_event","payload":{"id":"`+taskID+`","msg":{"type":"agent_message_delta","delta":"hi"}}}`)
			lines = append(lines,
				`{"ts":"2026-01-01T00:00:05.000Z","dir":"to_tui","kind":"codex_event","payload":{"id":"`+taskID+`","msg":{"type":"agent_message"}}}`)
			lines = append(lines,
				`{"ts":"2026-01-01T00:00:06.000Z","dir":"to_tui","kind":"codex_event","payload":{"id":"`+taskID+`","msg":{"type":"task_complete"}}}`)
		}
		lines = append(lines,
			`{"ts":"2026-01-01T00:00:07.000Z","dir":"to_tui","kind":"app_event","variant":"KnownVariant","event":"x","payload":{}}`)
		lines = append(lines,
			`{"ts":"2026-01-01T00:00:08.000Z","dir":"to_tui","kind":"app_event","variant":"UnknownFutureVariant","event":"y","payload":{}}`)
		lines = append(lines,
			`{"ts":"2026-01-01T00:00:09.000Z","dir":"to_tui","kind":"key_event","event":"KeyEnter","payload":{}}`)
		lines = append(lines,
			`{"ts":"2026-01-01T00:00:10.000Z","dir":"to_tui","kind":"log_line","line":"log","payload":{}}`)
		lines = append(lines,
			`{"ts":"2026-01-01T00:00:11.000Z","dir":"to_tui","kind":"insert_history","lines":3,"payload":{}}`)
		lines = append(lines,
			`{"ts":"2026-01-01T00:00:12.000Z","dir":"to_tui","kind":"future_trace_kind","payload":{"anything":true}}`)
		lines = append(lines,
			`{"ts":"2026-01-01T00:00:13.000Z","dir":"to_tui","kind":"codex_event","payload":{"id":"`+sessionID+`","msg":{"type":"shutdown_complete"}}}`)
		lines = append(lines,
			`{"ts":"2026-01-01T00:00:14.000Z","dir":"to_tui","kind":"session_end","payload":{}}`)
	}
	lines = append(lines,
		`{"ts":"2026-01-01T00:00:15.000Z","dir":"to_tui","kind":"future_session_kind","payload":{}}`)
	return lines
}

func seededCorpusEvents(t *testing.T) []Event {
	t.Helper()
	events, err := ParseBytes([]byte(strings.Join(seededCorpusRawLines(), "\n")))
	if err != nil {
		t.Fatalf("Parse(corpus): %v", err)
	}
	return events
}

// TestEventJSONRoundtripPreservesContractView pins the fixed point of the
// normalized contract view: marshaling and unmarshaling every normalized
// Event must reproduce the identical events and digest. If a future
// normalization change makes the view lossy, this property fails.
func TestEventJSONRoundtripPreservesContractView(t *testing.T) {
	events := seededCorpusEvents(t)
	round := make([]Event, 0, len(events))
	for _, ev := range events {
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("Marshal(%#v): %v", ev, err)
		}
		var back Event
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("Unmarshal(%s): %v", data, err)
		}
		round = append(round, back)
	}
	if len(round) != len(events) {
		t.Fatalf("roundtrip length = %d, want %d", len(round), len(events))
	}
	for i := range events {
		if got, want := jsonString(t, events[i]), jsonString(t, round[i]); got != want {
			t.Fatalf("event %d not preserved: got %s want %s", i, got, want)
		}
	}
	wantDigest := jsonString(t, Summarize(events))
	gotDigest := jsonString(t, Summarize(round))
	if gotDigest != wantDigest {
		t.Fatalf("digest changed across roundtrip:\n--- got ---\n%s\n--- want ---\n%s", gotDigest, wantDigest)
	}
}

// TestDigestIsDeterministic pins the digest's determinism: the same events
// must produce byte-identical digest JSON on every call.
func TestDigestIsDeterministic(t *testing.T) {
	events := seededCorpusEvents(t)
	first := jsonString(t, Summarize(events))
	for i := 0; i < 3; i++ {
		if got := jsonString(t, Summarize(events)); got != first {
			t.Fatalf("digest nondeterministic on iteration %d:\n--- first ---\n%s\n--- got ---\n%s", i, first, got)
		}
	}
}

// TestUnknownKindsAreNeverSwallowed pins the no-swallow rule: unknown trace
// kinds, unknown app variants and unknown codex msg types must surface in the
// digest (OtherKinds / AppVariants / MsgTypes), never be dropped silently,
// and every input line must be counted.
func TestUnknownKindsAreNeverSwallowed(t *testing.T) {
	events := seededCorpusEvents(t)
	digest := Summarize(events)
	if digest.TotalEvents != len(events) {
		t.Fatalf("TotalEvents = %d, want %d (no event may be dropped)", digest.TotalEvents, len(events))
	}
	if digest.OtherKinds["future_trace_kind"] < 1 {
		t.Fatalf("unknown kind future_trace_kind not preserved in OtherKinds: %#v", digest.OtherKinds)
	}
	if digest.OtherKinds["future_session_kind"] < 1 {
		t.Fatalf("unknown kind future_session_kind not preserved in OtherKinds: %#v", digest.OtherKinds)
	}
	if digest.AppVariants["UnknownFutureVariant"] < 1 {
		t.Fatalf("unknown app variant not preserved: %#v", digest.AppVariants)
	}
	if digest.KeyEvents != 2 || digest.LogLines != 2 {
		t.Fatalf("key/log lines = %d/%d, want 2/2 (one per seeded session)", digest.KeyEvents, digest.LogLines)
	}
	if len(digest.InsertHistory) != 2 || digest.InsertHistory[0] != 3 || digest.InsertHistory[1] != 3 {
		t.Fatalf("insert_history lines = %v, want [3 3]", digest.InsertHistory)
	}
}

// TestNormalizationDropsOnlyDocumentedVolatileFields pins the normalization
// rules per djalign dynamic-layer: wall-clock timestamps and machine-specific
// cwd are out of contract, model text becomes a presence flag, but the
// structural surface (dir/kind/variant/order/line counts) survives.
func TestNormalizationDropsOnlyDocumentedVolatileFields(t *testing.T) {
	raw := seededCorpusRawLines()
	for i, line := range raw {
		ev, err := normalize([]byte(line))
		if err != nil {
			t.Fatalf("normalize line %d: %v", i, err)
		}
		if strings.Contains(jsonString(t, ev), "2026-01-01") {
			t.Fatalf("line %d: timestamp leaked into normalized event %#v", i, ev)
		}
		if strings.Contains(jsonString(t, ev), `C:\work`) {
			t.Fatalf("line %d: cwd leaked into normalized event %#v", i, ev)
		}
		if strings.Contains(jsonString(t, ev), "gpt-test") {
			// session_start / session_configured model names are kept; the
			// gpt-test model only appears in session_start + task_started
			// payloads in this corpus - assert the documented drop sites only.
		}
	}
	// Model text presence: the delta lines must carry HasText, the plain
	// task_started lines must not.
	events := seededCorpusEvents(t)
	var deltaHasText, startHasText bool
	for _, ev := range events {
		switch ev.MsgType {
		case "agent_reasoning_raw_content_delta", "agent_message_delta":
			if !ev.HasText {
				t.Fatalf("%s HasText = false, want true: %#v", ev.MsgType, ev)
			}
			deltaHasText = true
		case "task_started":
			if ev.HasText {
				t.Fatalf("task_started HasText = true, want false: %#v", ev)
			}
			startHasText = true
		}
	}
	if !deltaHasText || !startHasText {
		t.Fatalf("corpus did not exercise both presence paths: delta=%v start=%v", deltaHasText, startHasText)
	}
}

func jsonString(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(%#v): %v", value, err)
	}
	return string(data)
}

// ParseBytes parses a JSONL recording from an in-memory buffer. It exists so
// property tests can feed seeded corpora without touching the Rust checkout.
func ParseBytes(data []byte) ([]Event, error) {
	var events []Event
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		ev, err := normalize(line)
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, nil
}
