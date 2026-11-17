package appserver

import "testing"

func statusHasFlag(status ThreadStatus, flag ThreadActiveFlag) bool {
	for _, candidate := range status.ActiveFlags {
		if candidate == flag {
			return true
		}
	}
	return false
}

// Mirrors Rust #61801: a terminal event for a turn that is no longer the latest
// must not cancel the newer turn's pending approval or clear its thread status.
func TestLateTurnCompletionPreservesNewerTurnApprovalWaitLikeRust(t *testing.T) {
	manager := NewThreadStatusManager()
	manager.NoteTurnStartedForTurn("thread-late", "turn-1")
	manager.NoteTurnStartedForTurn("thread-late", "turn-2")
	guard, _ := manager.NotePermissionRequestedWithNotification("thread-late")
	if status := manager.LoadedStatusForThread("thread-late"); !statusHasFlag(status, ThreadActiveFlagWaitingOnApproval) {
		t.Fatalf("newer turn status = %+v, want waitingOnApproval", status)
	}

	if notification := manager.NoteTurnCompletedForTurn("thread-late", "turn-1"); notification != nil {
		t.Fatalf("late completion emitted %+v, want no status change", notification)
	}
	if notification := manager.NoteTurnInterruptedForTurn("thread-late", "turn-1"); notification != nil {
		t.Fatalf("late interruption emitted %+v, want no status change", notification)
	}
	if status := manager.LoadedStatusForThread("thread-late"); !statusHasFlag(status, ThreadActiveFlagWaitingOnApproval) {
		t.Fatalf("late terminal event cleared the approval wait: %+v", status)
	}
	if got := manager.RunningTurnCount(); got != 1 {
		t.Fatalf("running turn count = %d, want the newer turn retained", got)
	}

	if notification := manager.NoteTurnCompletedForTurn("thread-late", "turn-2"); notification == nil {
		t.Fatal("latest turn completion did not change the status")
	}
	if status := manager.LoadedStatusForThread("thread-late"); status.Type != "idle" || len(status.ActiveFlags) != 0 {
		t.Fatalf("status after latest completion = %+v, want idle", status)
	}
	if got := manager.RunningTurnCount(); got != 0 {
		t.Fatalf("running turn count = %d, want 0", got)
	}
	_ = guard.Release()
}

// A manager that never recorded a started turn keeps the pre-#61801 behavior.
func TestTurnCompletionWithoutRecordedTurnKeepsBehaviorLikeRust(t *testing.T) {
	manager := NewThreadStatusManager()
	manager.NoteTurnStarted("thread-legacy")
	if notification := manager.NoteTurnCompleted("thread-legacy"); notification == nil {
		t.Fatal("completion without a recorded turn did not emit a status change")
	}
}
