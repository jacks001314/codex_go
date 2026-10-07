package rollout

import (
	"time"

	"codex_go/session"
)

// AppendUserGoalInstruction persists one explicit user goal instruction as a
// canonical response item.
//
// Mirrors Rust #49598
// (`app-server/src/request_processors/thread_goal_user_context.rs`): the
// instruction is recorded with `RolloutRecorder::record_canonical_items` before
// goal state changes, so an unloaded thread replays the same user-visible item.
// A raw item payload is written verbatim, preserving the host annotation on the
// message.
func (r *Recorder) AppendUserGoalInstruction(item session.Item, now time.Time) error {
	if r == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	line, err := LineFromItem(ItemFromSessionItem(&item), now)
	if err != nil {
		return err
	}
	return r.AppendLine(*line)
}
