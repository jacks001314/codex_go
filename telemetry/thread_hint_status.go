package telemetry

import (
	"context"
	"time"
)

// ThreadHintStatus mirrors Rust's analytics ThreadHintStatus (#42247).
type ThreadHintStatus string

const (
	ThreadHintSucceeded ThreadHintStatus = "succeeded"
	ThreadHintFailed    ThreadHintStatus = "failed"
)

// ThreadHintStatusEventParams carries one per-attempt history-notes hint
// outcome. Hint contents are never included (Rust #42247).
type ThreadHintStatusEventParams struct {
	ThreadID     string           `json:"thread_id"`
	SessionID    string           `json:"session_id"`
	Status       ThreadHintStatus `json:"status"`
	OccurredAtMS int64            `json:"occurred_at_ms"`
}

// ThreadHintStatusEvent is the analytics event produced for one hint attempt.
type ThreadHintStatusEvent struct {
	EventType   string                      `json:"event_type"`
	EventParams ThreadHintStatusEventParams `json:"event_params"`
}

// ThreadHintStatusEventSink is implemented by analytics clients that record
// history-notes hint outcomes.
type ThreadHintStatusEventSink interface {
	TrackThreadHintStatusEvent(context.Context, ThreadHintStatusEvent)
}

// NewThreadHintStatusEvent builds the event Rust's track_thread_hint_status
// emits.
func NewThreadHintStatusEvent(threadID string, sessionID string, status ThreadHintStatus, occurredAt time.Time) ThreadHintStatusEvent {
	return ThreadHintStatusEvent{
		EventType: "codex_thread_hint_status",
		EventParams: ThreadHintStatusEventParams{
			ThreadID:     threadID,
			SessionID:    sessionID,
			Status:       status,
			OccurredAtMS: occurredAt.UnixMilli(),
		},
	}
}
