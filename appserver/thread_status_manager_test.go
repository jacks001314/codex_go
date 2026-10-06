package appserver

import (
	"testing"
)

func TestManagerTracksStatusLifecycle(t *testing.T) {
	manager := NewThreadStatusManager()
	if got := manager.LoadedStatusForThread("thread-a"); got.Type != "notLoaded" {
		t.Fatalf("initial status = %#v", got)
	}
	manager.UpsertThread("thread-a", true)
	if got := manager.LoadedStatusForThread("thread-a"); got.Type != "idle" {
		t.Fatalf("upsert status = %#v", got)
	}
	manager.NoteTurnStarted("thread-a")
	if got := manager.LoadedStatusForThread("thread-a"); got.Type != "active" || len(got.ActiveFlags) != 0 {
		t.Fatalf("running status = %#v", got)
	}
	permission := manager.NotePermissionRequested("thread-a")
	userInput := manager.NoteUserInputRequested("thread-a")
	got := manager.LoadedStatusForThread("thread-a")
	if len(got.ActiveFlags) != 2 {
		t.Fatalf("pending status = %#v", got)
	}
	permission.Release()
	got = manager.LoadedStatusForThread("thread-a")
	if len(got.ActiveFlags) != 1 || got.ActiveFlags[0] != ThreadActiveFlagWaitingOnUserInput {
		t.Fatalf("after permission release = %#v", got)
	}
	userInput.Release()
	manager.NoteTurnCompleted("thread-a")
	if got := manager.LoadedStatusForThread("thread-a"); got.Type != "idle" {
		t.Fatalf("completed status = %#v", got)
	}
}

func TestManagerSystemErrorAndShutdown(t *testing.T) {
	manager := NewThreadStatusManager()
	manager.UpsertThread("thread-a", true)
	manager.NoteSystemError("thread-a")
	if got := manager.LoadedStatusForThread("thread-a"); got.Type != "systemError" {
		t.Fatalf("system error status = %#v", got)
	}
	manager.NoteTurnStarted("thread-a")
	if got := manager.LoadedStatusForThread("thread-a"); got.Type != "active" {
		t.Fatalf("restart status = %#v", got)
	}
	manager.NoteThreadShutdown("thread-a")
	if got := manager.LoadedStatusForThread("thread-a"); got.Type != "notLoaded" {
		t.Fatalf("shutdown status = %#v", got)
	}
}

func TestResolve(t *testing.T) {
	if got := ResolveThreadStatus(IdleStatus(), true); got.Type != "active" {
		t.Fatalf("Resolve idle = %#v", got)
	}
	if got := ResolveThreadStatus(ThreadStatus{Type: "systemError"}, true); got.Type != "systemError" {
		t.Fatalf("Resolve system error = %#v", got)
	}
}

// TestManagerTracksRunningTurnCountIncrementallyLikeRust mirrors Rust #49084:
// the running-turn count is maintained as runtimes change instead of rescanning
// the runtime map on every status mutation.
func TestManagerTracksRunningTurnCountIncrementallyLikeRust(t *testing.T) {
	manager := NewThreadStatusManager()
	if got := manager.RunningTurnCount(); got != 0 {
		t.Fatalf("initial running count = %d, want 0", got)
	}
	manager.NoteTurnStarted("thread-a")
	manager.NoteTurnStarted("thread-b")
	if got := manager.RunningTurnCount(); got != 2 {
		t.Fatalf("running count = %d, want 2", got)
	}
	// Duplicate starts must not double count.
	manager.NoteTurnStarted("thread-a")
	if got := manager.RunningTurnCount(); got != 2 {
		t.Fatalf("duplicate start count = %d, want 2", got)
	}
	// Completing a turn frees its slot.
	manager.NoteTurnCompleted("thread-a")
	if got := manager.RunningTurnCount(); got != 1 {
		t.Fatalf("after completion count = %d, want 1", got)
	}
	// Removing a running thread decrements the count.
	manager.RemoveThread("thread-b")
	if got := manager.RunningTurnCount(); got != 0 {
		t.Fatalf("after removal count = %d, want 0", got)
	}
	// Removing a non-running thread leaves the count stable.
	manager.NoteTurnStarted("thread-c")
	manager.NoteTurnCompleted("thread-c")
	manager.RemoveThread("thread-c")
	if got := manager.RunningTurnCount(); got != 0 {
		t.Fatalf("after idle removal count = %d, want 0", got)
	}
	// A system error also stops counting the thread as running.
	manager.NoteTurnStarted("thread-d")
	manager.NoteSystemError("thread-d")
	if got := manager.RunningTurnCount(); got != 0 {
		t.Fatalf("after system error count = %d, want 0", got)
	}
}
