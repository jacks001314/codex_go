package realtime

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func realtimeTestTranscriptBytes(entries []TranscriptEntry) int {
	total := 0
	for _, entry := range entries {
		total += realtimeTranscriptEntryBytes(entry)
	}
	return total
}

// The expectations below are the verbatim output of the upstream Rust functions
// for #54257 (`truncate_active_transcript` / `truncate_transcript_entry`,
// codex-api/src/endpoint/realtime_websocket/methods.rs:800-875) run on the same
// vectors by a standalone harness; the harness source and its raw output are
// recorded in update/r90c_2026_10_16.md. Every vector omits frame timestamps,
// which is the only shape the Go wire layer can produce, so Rust always takes
// its documented FIFO fallback ("preserving the legacy FIFO policy without
// timing", methods.rs:804).
func TestTrimRealtimeTranscriptMatchesRustFIFOFallback(t *testing.T) {
	cases := []struct {
		name      string
		entries   []TranscriptEntry
		want      []TranscriptEntry
		wantTotal int
	}{
		{
			name: "fifo drops the head utterance",
			entries: []TranscriptEntry{
				{Role: "user", Text: strings.Repeat("u", 4211)},
				{Role: "assistant", Text: strings.Repeat("a", 4000)},
			},
			want:      []TranscriptEntry{{Role: "assistant", Text: strings.Repeat("a", 4000)}},
			wantTotal: 4012,
		},
		{
			name:      "single oversized utterance keeps its tail behind the marker",
			entries:   []TranscriptEntry{{Role: "user", Text: strings.Repeat("u", 8192)}},
			want:      []TranscriptEntry{{Role: "user", Text: truncatedRealtimeTranscriptPrefix + strings.Repeat("u", 8182)}},
			wantTotal: 8192,
		},
		{
			name:      "cut moves forward to a utf-8 boundary",
			entries:   []TranscriptEntry{{Role: "user", Text: strings.Repeat("\u6c49", 3000)}},
			want:      []TranscriptEntry{{Role: "user", Text: truncatedRealtimeTranscriptPrefix + strings.Repeat("\u6c49", 2727)}},
			wantTotal: 8191,
		},
		{
			name: "fifo drops every utterance that does not fit",
			entries: []TranscriptEntry{
				{Role: "user", Text: strings.Repeat("u", 100)},
				{Role: "assistant", Text: strings.Repeat("a", 3000)},
				{Role: "user", Text: strings.Repeat("n", 6000)},
			},
			want:      []TranscriptEntry{{Role: "user", Text: strings.Repeat("n", 6000)}},
			wantTotal: 6007,
		},
		{
			name: "a transcript inside the budget is untouched",
			entries: []TranscriptEntry{
				{Role: "user", Text: "hello"},
				{Role: "assistant", Text: "world"},
			},
			want: []TranscriptEntry{
				{Role: "user", Text: "hello"},
				{Role: "assistant", Text: "world"},
			},
			wantTotal: 29,
		},
		{name: "empty transcript", entries: nil, want: nil, wantTotal: 0},
		{
			name: "the last oversized utterance keeps its tail",
			entries: []TranscriptEntry{
				{Role: "user", Text: strings.Repeat("u", 9000)},
				{Role: "assistant", Text: strings.Repeat("a", 9000)},
			},
			want:      []TranscriptEntry{{Role: "assistant", Text: truncatedRealtimeTranscriptPrefix + strings.Repeat("a", 8177)}},
			wantTotal: 8192,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			input := append([]TranscriptEntry(nil), testCase.entries...)
			before := append([]TranscriptEntry(nil), testCase.entries...)
			got := TrimRealtimeTranscript(input)
			for index := range before {
				if input[index] != before[index] {
					t.Fatalf("caller entry %d was mutated: %#v", index, input[index])
				}
			}
			if len(got) != len(testCase.want) {
				t.Fatalf("entries = %#v, want %#v", got, testCase.want)
			}
			for index := range got {
				if got[index].Role != testCase.want[index].Role || got[index].Text != testCase.want[index].Text {
					t.Fatalf("entry %d = role %q text(len %d), want role %q text(len %d)",
						index, got[index].Role, len(got[index].Text), testCase.want[index].Role, len(testCase.want[index].Text))
				}
				if !utf8.ValidString(got[index].Text) {
					t.Fatalf("entry %d kept an invalid utf-8 text", index)
				}
			}
			if total := realtimeTestTranscriptBytes(got); total != testCase.wantTotal {
				t.Fatalf("transcript bytes = %d, want %d", total, testCase.wantTotal)
			} else if total > maxActiveRealtimeTranscriptBytes {
				t.Fatalf("transcript bytes = %d, over the budget", total)
			}
		})
	}
}

// TestRealtimeTransportBoundsActiveTranscriptLikeRust drives the accumulator the
// same way a session does and checks that the budget holds, that eviction is
// FIFO from the head, and that the handoff cursor still points at the same
// conversation position after an already delivered utterance is evicted.
func TestRealtimeTransportBoundsActiveTranscriptLikeRust(t *testing.T) {
	session := &realtimeTransportSession{config: SessionConfig{FlushTranscriptTailOnEnd: true}}
	feed := func(event Event) Event {
		updated, _, _, _ := session.updateTranscript(event)
		if updated.Type != event.Type {
			t.Fatalf("live event changed: %#v", updated)
		}
		if total := realtimeTestTranscriptBytes(session.transcript); total > maxActiveRealtimeTranscriptBytes {
			t.Fatalf("accumulator holds %d bytes, over the budget", total)
		}
		return updated
	}
	feed(Event{Type: "input_transcript.delta", Delta: strings.Repeat("u", 4000)})
	feed(Event{Type: "output_transcript.delta", Delta: strings.Repeat("a", 4000)})
	feed(Event{Type: "input_transcript.delta", Delta: strings.Repeat("n", 6000)})
	if len(session.transcript) != 1 || session.transcript[0].Role != "user" || len(session.transcript[0].Text) != 6000 {
		t.Fatalf("accumulator = %#v, want only the newest utterance", session.transcript)
	}

	handoff := feed(Event{Type: "handoff.requested", InputTranscript: "delegate now"})
	if total := realtimeTestTranscriptBytes(handoff.ActiveTranscript); total > maxActiveRealtimeTranscriptBytes {
		t.Fatalf("handoff active transcript = %d bytes, over the budget", total)
	}
	if len(handoff.ActiveTranscript) != 2 ||
		handoff.ActiveTranscript[0].Role != "user" || len(handoff.ActiveTranscript[0].Text) != 6000 ||
		handoff.ActiveTranscript[1].Text != "delegate now" {
		t.Fatalf("handoff active transcript = %#v", handoff.ActiveTranscript)
	}

	feed(Event{Type: "output_transcript.delta", Delta: strings.Repeat("a", 8000)})
	tail := session.takeTranscriptTail()
	if len(tail) != 1 || tail[0].Role != "assistant" || len(tail[0].Text) != 8000 {
		t.Fatalf("tail = %#v, want only the newest assistant utterance", tail)
	}
	if total := realtimeTestTranscriptBytes(tail); total > maxActiveRealtimeTranscriptBytes {
		t.Fatalf("tail holds %d bytes, over the budget", total)
	}
	if second := session.takeTranscriptTail(); len(second) != 0 {
		t.Fatalf("second tail = %#v, want empty", second)
	}
}
