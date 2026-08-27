package appserver

import "testing"

func TestShouldEmitThreadStartedNotificationHidesMemoryConsolidation(t *testing.T) {
	// Persistent threads are always announced.
	if !shouldEmitThreadStartedNotification(&Thread{Ephemeral: false}) {
		t.Fatal("persistent thread should be announced")
	}
	// Ephemeral non-internal threads are announced (e.g. subagents shown in the
	// agents overview).
	subagent := ThreadSourceSubagent
	if !shouldEmitThreadStartedNotification(&Thread{Ephemeral: true, ThreadSource: &subagent}) {
		t.Fatal("ephemeral subagent thread should be announced")
	}
	// Ephemeral memory-consolidation helper threads are hidden (Rust #40494).
	memSource := ThreadSourceMemoryConsolidation
	if shouldEmitThreadStartedNotification(&Thread{Ephemeral: true, ThreadSource: &memSource}) {
		t.Fatal("ephemeral memory-consolidation thread should be hidden")
	}
}
