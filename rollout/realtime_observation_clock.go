package rollout

import (
	"encoding/json"

	"codex_go/realtime"
)

// ObservationClockFloor returns the newest canonical realtime observation time
// or compaction checkpoint floor recorded in the rollout. The floor keeps
// first-observed timestamps non-decreasing across clock rollback and cold
// resume (Rust #61473 ObservationClock::restore).
func ObservationClockFloor(lines []Line) *int64 {
	var floor *int64
	observe := func(value *int64) {
		if value == nil {
			return
		}
		if floor == nil || *value > *floor {
			copied := *value
			floor = &copied
		}
	}
	for i := range lines {
		line := &lines[i]
		switch line.Type {
		case "realtime_item":
			payload := line.RealtimeItem
			if len(payload) == 0 {
				payload = line.Payload
			}
			if len(payload) == 0 {
				continue
			}
			var item realtime.RealtimeItem
			if err := item.UnmarshalRolloutJSON(payload); err != nil {
				continue
			}
			observe(item.ObservedAtMS)
		case "item", "response_item":
			// Rust #62251: a persisted model message keeps its realtime
			// observation time beside the message, so restoring the clock after a
			// restart does not depend on a separate receipt row.
			payload := line.Item
			if len(payload) == 0 {
				payload = line.Payload
			}
			if len(payload) == 0 {
				continue
			}
			observe(realtime.RealtimeObservedAtMS(line.ItemMetadata))
		case "compacted":
			if len(line.Payload) == 0 {
				continue
			}
			var payload struct {
				ResumeMetadata *CompactionResumeMetadata `json:"resume_metadata"`
			}
			if err := json.Unmarshal(line.Payload, &payload); err != nil {
				continue
			}
			if payload.ResumeMetadata == nil {
				continue
			}
			observe(payload.ResumeMetadata.ObservationClockFloorMS)
		}
	}
	return floor
}
