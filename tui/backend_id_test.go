package tui

import "testing"

// TestBackendIDIdentityLikeRust mirrors Rust #54681 (backend_id.rs): the
// identity is process-local, a fresh one never equals another, and the
// process owner stays stable across calls.
func TestBackendIDIdentityLikeRust(t *testing.T) {
	first := ProcessBackendID()
	if first.IsZero() {
		t.Fatal("ProcessBackendID() is zero")
	}
	if again := ProcessBackendID(); again != first {
		t.Fatalf("ProcessBackendID() = %v then %v, want a stable launch owner", first, again)
	}
	other := NewBackendID()
	if other == first {
		t.Fatalf("NewBackendID() = %v, want an identity distinct from the process owner", other)
	}
	var zero BackendID
	if !zero.IsZero() {
		t.Fatalf("zero BackendID IsZero() = false")
	}
}

// TestSessionTargetEnsureBackendLikeRust mirrors
// resume_picker::SessionTarget::ensure_backend: a selection from another
// backend is refused with the picker guidance before any request is sent.
func TestSessionTargetEnsureBackendLikeRust(t *testing.T) {
	owner := NewBackendID()
	foreign := NewBackendID()
	target := SessionTarget{BackendID: owner, ThreadID: "thread-1"}
	if err := target.EnsureBackend(owner); err != nil {
		t.Fatalf("EnsureBackend(owner) error = %v", err)
	}
	err := target.EnsureBackend(foreign)
	if err == nil {
		t.Fatal("EnsureBackend(foreign) = nil, want a backend mismatch")
	}
	const want = "This session belongs to a different backend. Open the session picker again."
	if err.Error() != want {
		t.Fatalf("EnsureBackend(foreign) error = %q, want %q", err, want)
	}
}

// TestSessionPickerStampsBackendLikeRust mirrors
// resume_picker::SessionPickerRunOptions::backend_id: every selection the
// picker produces carries the backend that listed the sessions, even when the
// thread IDs match a session owned elsewhere.
func TestSessionPickerStampsBackendLikeRust(t *testing.T) {
	owner := NewBackendID()
	picker := NewSessionPickerStateForBackend(owner, SessionPickerResume, []SessionSummary{{
		ThreadID: "thread-1",
		Path:     "/tmp/rollout.jsonl",
	}}, "")
	selection, ok := picker.Selection()
	if !ok {
		t.Fatal("Selection() = false")
	}
	if selection.Target.BackendID != owner {
		t.Fatalf("selection backend = %v, want %v", selection.Target.BackendID, owner)
	}
	if err := selection.Target.EnsureBackend(NewBackendID()); err == nil {
		t.Fatal("a picker target was accepted by another backend")
	}
	// The default constructor binds to this process's backend.
	if plain := NewSessionPickerState(SessionPickerResume, []SessionSummary{{ThreadID: "thread-1"}}, ""); plain.BackendID != ProcessBackendID() {
		t.Fatalf("NewSessionPickerState backend = %v, want the process owner", plain.BackendID)
	}
}
