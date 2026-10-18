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
		if total := realtimeTestTranscriptBytes(session.transcript.plainEntries()); total > maxActiveRealtimeTranscriptBytes {
			t.Fatalf("accumulator holds %d bytes, over the budget", total)
		}
		return updated
	}
	feed(Event{Type: "input_transcript.delta", Delta: strings.Repeat("u", 4000)})
	feed(Event{Type: "output_transcript.delta", Delta: strings.Repeat("a", 4000)})
	feed(Event{Type: "input_transcript.delta", Delta: strings.Repeat("n", 6000)})
	entries := session.transcript.plainEntries()
	if len(entries) != 1 || entries[0].Role != "user" || len(entries[0].Text) != 6000 {
		t.Fatalf("accumulator = %#v, want only the newest utterance", entries)
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

// These vectors are the Rust #54257 transcript regression table. They cover
// raw-frame recency, both speakers in one frame, UTF-8 suffix cuts, delayed
// finals extending a retained suffix, and the untimed FIFO fallback.
func TestRealtimeTransportPreservesRecentSpeechByFrameLikeRust(t *testing.T) {
	frame := func(startMS uint64) *realtimeTranscriptFrame {
		return &realtimeTranscriptFrame{startMS: startMS, endMS: startMS + 200}
	}
	type step struct {
		role  string
		text  string
		frame *realtimeTranscriptFrame
		final bool
	}

	oldUser := strings.Repeat("u", 4000)
	assistant := strings.Repeat("a", 4000)
	newestUser := strings.Repeat("n", 211)
	sameFrameUser := strings.Repeat("n", 22)
	unicodeUser := strings.Repeat("\u00e9", 2000)
	oversizedUser := strings.Repeat("u", maxActiveRealtimeTranscriptBytes)

	cases := []struct {
		name  string
		steps []step
		want  []TranscriptEntry
	}{
		{
			name: "evicted assistant does not return newer after a delayed final",
			steps: []step{
				{role: "user", text: oldUser, frame: frame(0)},
				{role: "assistant", text: assistant, frame: frame(200)},
				{role: "user", text: newestUser, frame: frame(400)},
				{role: "user", text: "", frame: frame(600)},
				{role: "assistant", text: assistant, final: true},
			},
			want: []TranscriptEntry{{Role: "user", Text: oldUser + newestUser}},
		},
		{
			name: "same frame input and output keep both suffixes",
			steps: []step{
				{role: "assistant", text: assistant, frame: frame(0)},
				{role: "user", text: strings.Repeat("u", 4160), frame: frame(200)},
				{role: "user", text: sameFrameUser, frame: frame(400)},
				{role: "assistant", text: strings.Repeat("b", 20), frame: frame(400)},
			},
			want: []TranscriptEntry{
				{Role: "assistant", Text: assistant + strings.Repeat("b", 20)},
				{Role: "user", Text: truncatedRealtimeTranscriptPrefix + strings.Repeat("u", 4128) + sameFrameUser},
			},
		},
		{
			name: "output arriving after input in the same frame does not become newer",
			steps: []step{
				{role: "user", text: oldUser, frame: frame(0)},
				{role: "assistant", text: assistant, frame: frame(200)},
				{role: "user", text: newestUser, frame: frame(400)},
				{role: "assistant", text: "b", frame: frame(400)},
			},
			want: []TranscriptEntry{
				{Role: "user", Text: truncatedRealtimeTranscriptPrefix + strings.Repeat("u", 3958) + newestUser},
				{Role: "assistant", Text: assistant + "b"},
			},
		},
		{
			name: "reversing same-frame events keeps the same suffixes",
			steps: []step{
				{role: "user", text: oldUser, frame: frame(0)},
				{role: "assistant", text: assistant, frame: frame(200)},
				{role: "assistant", text: "b", frame: frame(400)},
				{role: "user", text: newestUser, frame: frame(400)},
				{role: "user", text: "", frame: frame(600)},
			},
			want: []TranscriptEntry{
				{Role: "user", Text: truncatedRealtimeTranscriptPrefix + strings.Repeat("u", 3958) + newestUser},
				{Role: "assistant", Text: assistant + "b"},
			},
		},
		{
			name: "tied frame suffixes move the cut to a UTF-8 boundary",
			steps: []step{
				{role: "user", text: unicodeUser, frame: frame(0)},
				{role: "assistant", text: assistant, frame: frame(200)},
				{role: "user", text: newestUser, frame: frame(400)},
				{role: "assistant", text: newestUser, frame: frame(400)},
			},
			want: []TranscriptEntry{
				{Role: "user", Text: truncatedRealtimeTranscriptPrefix + strings.Repeat("\u00e9", 1937) + newestUser},
				{Role: "assistant", Text: truncatedRealtimeTranscriptPrefix + strings.Repeat("a", 3871) + newestUser},
			},
		},
		{
			name: "an oversized utterance is bounded before the same-frame peer arrives",
			steps: []step{
				{role: "user", text: oversizedUser, frame: frame(0)},
				{role: "assistant", text: "b", frame: frame(0)},
			},
			want: []TranscriptEntry{
				{Role: "user", Text: truncatedRealtimeTranscriptPrefix + strings.Repeat("u", 8169)},
				{Role: "assistant", Text: "b"},
			},
		},
		{
			name: "a delayed final extends the retained suffix without changing age",
			steps: []step{
				{role: "assistant", text: assistant, frame: frame(0)},
				{role: "user", text: oldUser, frame: frame(200)},
				{role: "assistant", text: newestUser, frame: frame(400)},
				{role: "user", text: "b", frame: frame(400)},
				{role: "user", text: "", frame: frame(600)},
				{role: "assistant", text: assistant + newestUser + "!", final: true},
			},
			want: []TranscriptEntry{
				{Role: "assistant", Text: truncatedRealtimeTranscriptPrefix + strings.Repeat("a", 3957) + newestUser + "!"},
				{Role: "user", Text: oldUser + "b"},
			},
		},
		{
			name: "untimed utterances retain the FIFO fallback",
			steps: []step{
				{role: "user", text: oldUser},
				{role: "assistant", text: assistant},
				{role: "user", text: newestUser},
			},
			want: []TranscriptEntry{{Role: "assistant", Text: assistant}},
		},
		{
			name: "same-frame speakers retain conversation order",
			steps: []step{
				{role: "assistant", text: "I will", frame: frame(0)},
				{role: "user", text: "Stop!", frame: frame(200)},
				{role: "assistant", text: " do it", frame: frame(200)},
				{role: "assistant", text: "I will do it", final: true},
			},
			want: []TranscriptEntry{
				{Role: "assistant", Text: "I will do it"},
				{Role: "user", Text: "Stop!"},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			session := &realtimeTransportSession{config: SessionConfig{Version: VersionV3}}
			for _, step := range testCase.steps {
				eventType := "input_transcript.delta"
				if step.role == "assistant" {
					eventType = "output_transcript.delta"
				}
				if step.final {
					eventType = "output_transcript.done"
					if step.role == "user" {
						eventType = "input_transcript.done"
					}
				}
				event := Event{Type: eventType, Delta: step.text, Text: step.text, transcriptFrame: step.frame}
				updated, _, _, _ := session.updateTranscript(event)
				if updated.Type != event.Type || updated.Delta != event.Delta || updated.Text != event.Text {
					t.Fatalf("live event changed: %#v", updated)
				}
			}
			session.transcript.trim()
			got := session.transcript.takeEntries()
			if len(got) != len(testCase.want) {
				t.Fatalf("entries = %#v, want %#v", got, testCase.want)
			}
			for index := range got {
				if got[index] != testCase.want[index] {
					t.Fatalf(
						"entry %d = role %q text len %d prefix %q suffix %q; want role %q text len %d prefix %q suffix %q",
						index,
						got[index].Role,
						len(got[index].Text),
						textPrefix(got[index].Text),
						textSuffix(got[index].Text),
						testCase.want[index].Role,
						len(testCase.want[index].Text),
						textPrefix(testCase.want[index].Text),
						textSuffix(testCase.want[index].Text),
					)
				}
			}
			if total := realtimeTestTranscriptBytes(got); total > maxActiveRealtimeTranscriptBytes {
				t.Fatalf("transcript bytes = %d, over the budget", total)
			}
		})
	}
}

func textPrefix(text string) string {
	const limit = 8
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

func textSuffix(text string) string {
	const limit = 8
	if len(text) <= limit {
		return text
	}
	return "..." + text[len(text)-limit:]
}

func TestParseRealtimeTranscriptFrameLikeRust(t *testing.T) {
	frame, ok := parseRealtimeWireEvent(VersionV3, []byte(
		`{"type":"input_transcript.added","start_ms":400,"end_ms":600,"item":{"text":"hello"}}`,
	))
	if !ok || frame.transcriptFrame == nil ||
		frame.transcriptFrame.startMS != 400 || frame.transcriptFrame.endMS != 600 {
		t.Fatalf("parsed frame = %#v, ok = %v", frame.transcriptFrame, ok)
	}
	untimed, ok := parseRealtimeWireEvent(VersionV3, []byte(
		`{"type":"output_transcript.added","start_ms":400,"item":{"text":"hello"}}`,
	))
	if !ok || untimed.transcriptFrame != nil {
		t.Fatalf("partial frame = %#v, ok = %v", untimed.transcriptFrame, ok)
	}
}
