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
