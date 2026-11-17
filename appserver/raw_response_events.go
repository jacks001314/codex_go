package appserver

import (
	"strings"

	"codex_go/session"
	"codex_go/turn"
)

const experimentalRawEventsExtraKey = "experimental_raw_events"

func threadRecordExperimentalRawEvents(record *session.Record) bool {
	return record != nil && boolFromMap(record.Metadata.Extra, experimentalRawEventsExtraKey)
}

// emitRecordedRawResponseItems mirrors Rust's `send_raw_response_items`
// (`core/src/session/mod.rs`): every recorded conversation item is announced as
// `RawResponseItem` on the thread's stream. The app server forwards those events
// only to threads that opted into experimental raw events
// (`thread_lifecycle.rs` drops `RawResponseItem` otherwise), so clients without
// the opt-in never see them.
func (r *RuntimeRouter) emitRecordedRawResponseItems(threadID session.ThreadID, items []session.Item) {
	if r == nil || len(items) == 0 {
		return
	}
	record, err := r.threadRecord(threadID, true, false)
	if err != nil || !threadRecordExperimentalRawEvents(record) {
		return
	}
	for i := range items {
		payload := session.InputItemFromItem(&items[i], &session.HistoryBuildOptions{IncludeToolOutputs: true})
		if payload == nil {
			continue
		}
		// Rust keeps warehouse metadata out of app-server notifications
		// (`clear_executed_tool_calls`), so the announced item matches what the
		// client is allowed to see.
		turn.StripDirectCallMetadata([]any{payload})
		r.notify(NotificationRawResponseItemCompleted, &RawResponseItemCompletedNotification{
			ThreadID: string(threadID),
			TurnID:   recordedRawResponseItemTurnID(&items[i]),
			Item:     payload,
		})
	}
}

func recordedRawResponseItemTurnID(item *session.Item) string {
	if item == nil {
		return ""
	}
	for _, key := range []string{"turnId", "turn_id"} {
		if value := strings.TrimSpace(stringFromMap(item.Metadata, key)); value != "" {
			return value
		}
	}
	return ""
}
