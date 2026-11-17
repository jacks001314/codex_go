package appserver

import (
	"testing"

	"codex_go/telemetry"
)

// Rust analytics (#60018) keeps the turn trigger on the turn's analytics
// metadata, so a usage event that finishes after the turn still reports it.
func TestAnalyticsTurnTriggerRetainedForUsageEventsLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	if got := router.analyticsTurnTriggerForTurn("thread-1", "turn-1"); got != nil {
		t.Fatalf("unregistered turn trigger = %q, want none", *got)
	}
	router.recordAnalyticsTurnTrigger("thread-2", "turn-2", "   ")
	if got := router.analyticsTurnTriggerForTurn("thread-2", "turn-2"); got != nil {
		t.Fatalf("blank trigger = %q, want none", *got)
	}

	router.recordAnalyticsTurnTrigger("thread-1", "turn-1", "automation_cron_scheduled")
	trigger := router.analyticsTurnTriggerForTurn("thread-1", "turn-1")
	if trigger == nil || *trigger != "automation_cron_scheduled" {
		t.Fatalf("retained trigger = %v, want automation_cron_scheduled", trigger)
	}
	if got := router.analyticsTurnTriggerForTurn("thread-1", "turn-2"); got != nil {
		t.Fatalf("another turn must not inherit the trigger, got %q", *got)
	}

	// Tool events pick the trigger up when they are built.
	base := telemetry.CodexToolItemEventBase{ThreadID: "thread-1", TurnID: "turn-1", ItemID: "item-1"}
	router.enrichToolEventBase(&base, "thread-1", "turn-1")
	if base.TurnTrigger == nil || *base.TurnTrigger != "automation_cron_scheduled" {
		t.Fatalf("tool event trigger = %v, want automation_cron_scheduled", base.TurnTrigger)
	}
	pinned := "queue"
	explicit := telemetry.CodexToolItemEventBase{TurnTrigger: &pinned, ItemID: "item-2"}
	router.enrichToolEventBase(&explicit, "thread-1", "turn-1")
	if explicit.TurnTrigger == nil || *explicit.TurnTrigger != "queue" {
		t.Fatalf("an explicit trigger must win, got %v", explicit.TurnTrigger)
	}

	router.discardAnalyticsTurn("thread-1", "turn-1")
	if got := router.analyticsTurnTriggerForTurn("thread-1", "turn-1"); got != nil {
		t.Fatalf("discarded turn trigger = %q, want none", *got)
	}
}
