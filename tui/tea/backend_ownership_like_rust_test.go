package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// TestThreadBackendKeepsFirstObservedOwnerLikeRust pins Rust #60929
// BackendRegistry::observe: a conversation keeps the backend that first served
// it and a conflicting observation is rejected instead of taking ownership.
func TestThreadBackendKeepsFirstObservedOwnerLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 30})
	first := codextui.NewBackendID()
	second := codextui.NewBackendID()

	if !model.noteThreadBackend("thread-1", first) {
		t.Fatal("first observation was rejected")
	}
	if !model.noteThreadBackend("thread-1", first) {
		t.Fatal("repeated observation by the owner was rejected")
	}
	if model.noteThreadBackend("thread-1", second) {
		t.Fatal("conflicting backend took ownership")
	}
	owner, ok := model.threadBackendOwner("thread-1")
	if !ok || owner != first {
		t.Fatalf("owner = %v, %v, want the first backend", owner, ok)
	}
	if model.noteThreadBackend("", first) || model.noteThreadBackend("thread-2", codextui.BackendID{}) {
		t.Fatal("unusable observation was accepted")
	}
}

// TestSelectionBackendPrefersRecordedOwnerLikeRust keeps a stale listing stamp
// from routing a thread through another backend.
func TestSelectionBackendPrefersRecordedOwnerLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 30})
	owner := codextui.NewBackendID()
	stale := codextui.NewBackendID()
	model.noteThreadBackend("thread-1", owner)
	target := codextui.SessionTarget{ThreadID: "thread-1", BackendID: stale}
	if got := model.selectionBackend(target); got != owner {
		t.Fatalf("selection backend = %v, want the recorded owner %v", got, owner)
	}
}
