package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/telemetry"
)

type threadHintStatusRecorder struct {
	events []telemetry.ThreadHintStatusEvent
}

func (r *threadHintStatusRecorder) TrackThreadHintStatusEvent(_ context.Context, event telemetry.ThreadHintStatusEvent) {
	r.events = append(r.events, event)
}

// TestHistoryNotesThreadHintStatusLikeRust mirrors Rust #42247: every attempt
// reports succeeded/failed with the thread/session identity and timestamp, and
// hint contents never reach the event.
func TestHistoryNotesThreadHintStatusLikeRust(t *testing.T) {
	recorder := &threadHintStatusRecorder{}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	trackHistoryNotesThreadHintStatus(recorder, "thread-1", "session-1", "secret hint text", true, now)
	trackHistoryNotesThreadHintStatus(recorder, "thread-1", "session-1", "", false, now)
	if len(recorder.events) != 2 {
		t.Fatalf("events = %#v, want two attempts", recorder.events)
	}
	first, second := recorder.events[0].EventParams, recorder.events[1].EventParams
	if first.Status != telemetry.ThreadHintSucceeded || second.Status != telemetry.ThreadHintFailed {
		t.Fatalf("statuses = %q/%q", first.Status, second.Status)
	}
	if first.ThreadID != "thread-1" || first.SessionID != "session-1" || first.OccurredAtMS != now.UnixMilli() {
		t.Fatalf("event identity = %#v", first)
	}
	// The event carries only the outcome; a sink without the interface is a
	// no-op rather than a panic.
	trackHistoryNotesThreadHintStatus(struct{}{}, "thread-1", "session-1", "hint", true, now)
}
