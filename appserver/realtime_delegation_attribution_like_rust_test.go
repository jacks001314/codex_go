package appserver

import (
	"strings"
	"testing"

	"codex_go/realtime"
)

// Rust #62288 RealtimeDelegation::from_events: rendering and attribution share
// one pass, so only the transcript frames the rendered body carries are
// accepted, and the event's own id is the fallback when no frame is named.
func TestRealtimeHandoffAttributesRenderedFramesLikeRust(t *testing.T) {
	entries := []realtime.TranscriptEntry{
		{Role: "user", Text: "speak", ID: "frame-1"},
		{Role: "assistant", Text: "   ", ID: "frame-blank"},
		{Role: "assistant", Text: "reply", ID: "frame-2"},
		{Role: "user", Text: "again", ID: "frame-1"},
	}
	delegation, frames := realtimeDelegationAttribution("", entries)
	if delegation == "" {
		t.Fatal("delegation was empty")
	}
	if !strings.Contains(delegation, "user: speak") || !strings.Contains(delegation, "assistant: reply") || !strings.Contains(delegation, "user: again") {
		t.Fatalf("delegation did not render every spoken row: %q", delegation)
	}
	if strings.Contains(delegation, "frame-1") || strings.Contains(delegation, "frame-blank") || strings.Contains(delegation, "frame-2") {
		t.Fatalf("delegation leaked a frame id: %q", delegation)
	}
	sources := realtimeAttributedSources(realtime.Event{ItemID: "event-1"}, frames)
	want := []string{"frame-1", "frame-2"}
	if len(sources) != len(want) || sources[0] != want[0] || sources[1] != want[1] {
		t.Fatalf("attributed sources = %#v, want %#v", sources, want)
	}

	fallback := realtimeAttributedSources(realtime.Event{ItemID: "event-1"}, nil)
	if len(fallback) != 1 || fallback[0] != "event-1" {
		t.Fatalf("fallback sources = %#v, want the event id", fallback)
	}

	deduped := realtimeAttributedSources(realtime.Event{}, []string{"a", "a", "b"})
	if len(deduped) != 2 || deduped[0] != "a" || deduped[1] != "b" {
		t.Fatalf("deduped sources = %#v", deduped)
	}
}
