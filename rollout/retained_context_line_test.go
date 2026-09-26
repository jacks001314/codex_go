package rollout

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/retainedctx"
)

// Mirrors Rust's RolloutItem::RetainedContext: the sparse fact is written under
// the `payload` key of a `retained_context` line, with the internally tagged
// `verified_answer` body, and a reload returns it in order.
func TestAppendRetainedContextWritesRustWireLikeRust(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home,
		ThreadID:  "thread-retained-line",
		Now:       now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	acceptance := uint64(4)
	event := retainedctx.RetainedContextEvent{
		Answer: retainedctx.VerifiedAnswer{
			TurnID: "turn-1",
			CallID: "call-1",
			Questions: []retainedctx.VerifiedQuestionAnswer{
				{Question: "Pick one?\nB: Second", Answer: "B"},
			},
		},
		AcceptanceOrder: &acceptance,
	}
	if err := recorder.AppendRetainedContext(event, now); err != nil {
		t.Fatalf("AppendRetainedContext() error = %v", err)
	}
	if err := recorder.AppendTurnContext(TurnContextRecord{TurnID: "turn-1", Model: "gpt-5", CompHash: "hash"}, now); err != nil {
		t.Fatalf("AppendTurnContext() error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, want := range []string{
		`"type":"retained_context"`,
		`"payload":{"type":"verified_answer","turn_id":"turn-1","call_id":"call-1"`,
		`"acceptance_order":4`,
		// Rust's RolloutItemWire serializes a turn context under `payload` too.
		`"type":"turn_context"`,
		`"payload":{"turn_id":"turn-1","model":"gpt-5","comp_hash":"hash"}`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("rollout is missing %s:\n%s", want, content)
		}
	}
	if strings.Contains(content, `"turn_context":{`) {
		t.Fatalf("turn context was written with a Go-only key:\n%s", content)
	}

	lines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	events := RetainedContextEvents(lines)
	if len(events) != 1 {
		t.Fatalf("retained events = %#v", events)
	}
	if !reflect.DeepEqual(events[0].Answer, event.Answer) {
		t.Fatalf("reloaded answer = %#v, want %#v", events[0].Answer, event.Answer)
	}
	if events[0].AcceptanceOrder == nil || *events[0].AcceptanceOrder != acceptance {
		t.Fatalf("reloaded acceptance order = %v, want %d", events[0].AcceptanceOrder, acceptance)
	}
}

// A rollout written by the Rust implementation carries the same fact under the
// `payload` key, so the reader returns it without a Go-written line.
func TestRetainedContextEventsReadsRustWrittenLinesLikeRust(t *testing.T) {
	lines := []Line{
		{Type: "session_meta"},
		{
			Type:    "retained_context",
			Payload: []byte(`{"type":"verified_answer","turn_id":"turn-2","call_id":"call-2","questions":[{"question":"Proceed?","answer":"yes"}]}`),
		},
		{Type: "retained_context", Payload: []byte(`not json`)},
		{Type: "turn_context", Payload: []byte(`{"turn_id":"turn-2","model":"gpt-5"}`)},
	}
	events := RetainedContextEvents(lines)
	if len(events) != 1 {
		t.Fatalf("retained events = %#v", events)
	}
	if events[0].Answer.TurnID != "turn-2" || events[0].Answer.CallID != "call-2" ||
		len(events[0].Answer.Questions) != 1 || events[0].Answer.Questions[0].Answer != "yes" {
		t.Fatalf("retained event = %#v", events[0])
	}
	if events[0].AcceptanceOrder != nil {
		t.Fatalf("legacy event carried an acceptance order: %v", events[0].AcceptanceOrder)
	}
}
