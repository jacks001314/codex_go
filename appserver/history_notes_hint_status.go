package appserver

import (
	"context"
	"strings"
	"time"

	"codex_go/telemetry"
)

// trackHistoryNotesThreadHintStatus mirrors Rust's
// `track_thread_hint_status` (#42247): report one per-attempt outcome without
// hint contents. A missing/oversized/empty hint is a failed attempt.
func trackHistoryNotesThreadHintStatus(sink any, threadID string, sessionID string, text string, ok bool, now time.Time) {
	recorder, _ := sink.(telemetry.ThreadHintStatusEventSink)
	if recorder == nil {
		return
	}
	status := telemetry.ThreadHintFailed
	if ok && strings.TrimSpace(text) != "" {
		status = telemetry.ThreadHintSucceeded
	}
	recorder.TrackThreadHintStatusEvent(context.Background(), telemetry.NewThreadHintStatusEvent(threadID, sessionID, status, now))
}
