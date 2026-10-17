package app

import (
	"testing"

	codextui "codex_go/tui"
)

const foreignBackendSelectionError = "This session belongs to a different backend. Open the session picker again."

// TestSessionSelectionBackendOwnershipLikeRust mirrors Rust #54681
// (app/tests/backend_ownership_tests.rs): a selection whose thread ID matches
// but whose backend does not is rejected before any session state changes or
// resume/fork/unarchive request is sent.
func TestSessionSelectionBackendOwnershipLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	foreign := codextui.NewBackendID()
	target := codextui.SessionTarget{BackendID: foreign, ThreadID: "thread-owned-elsewhere"}

	_, err := interactiveResumeSessionHandler(nil)(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: target,
	})
	if err == nil || err.Error() != foreignBackendSelectionError {
		t.Fatalf("resume error = %v, want %q", err, foreignBackendSelectionError)
	}

	for _, kind := range []codextui.SessionSelectionKind{
		codextui.SessionSelectionFork,
		codextui.SessionSelectionArchive,
		codextui.SessionSelectionUnarchive,
		codextui.SessionSelectionDelete,
	} {
		_, err := interactiveSessionActionHandler(nil)(codextui.SessionSelection{Kind: kind, Target: target})
		if err == nil || err.Error() != foreignBackendSelectionError {
			t.Fatalf("%s error = %v, want %q", kind, err, foreignBackendSelectionError)
		}
	}

	// A selection from this process's backend clears the guard and reaches the
	// store; the unknown thread is reported by the store, not as a foreign
	// selection.
	_, err = interactiveResumeSessionHandler(nil)(codextui.SessionSelection{
		Kind: codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{
			BackendID: codextui.ProcessBackendID(),
			ThreadID:  "thread-not-found",
		},
	})
	if err != nil && err.Error() == foreignBackendSelectionError {
		t.Fatalf("local selection was rejected as foreign: %v", err)
	}
}
