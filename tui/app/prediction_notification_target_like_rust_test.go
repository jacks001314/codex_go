package app

import "testing"

// Mirrors Rust #49480: a prediction notification is routed to the thread it
// names, exactly like the other thread-scoped goal/name notifications.
func TestPredictionNotificationTargetsItsThreadLikeRust(t *testing.T) {
	target := ServerNotificationThreadTargetForEvent(&ServerEvent{
		Name:     ServerNotificationThreadPredictionUpdated,
		ThreadID: rustThreadID1,
	})
	if target.Kind != ServerNotificationThreadTargetThread || target.ThreadID != rustThreadID1 {
		t.Fatalf("target = %#v", target)
	}
	if target := ServerNotificationThreadTargetForEvent(&ServerEvent{
		Name:     ServerNotificationThreadPredictionUpdated,
		ThreadID: "not-a-thread",
	}); target.Kind != ServerNotificationThreadTargetInvalid {
		t.Fatalf("invalid target = %#v", target)
	}
}
