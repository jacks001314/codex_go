package app

import (
	"testing"

	"codex_go/session"
	codextui "codex_go/tui"
)

// TestInteractiveResumeAndForkRejectArchivedSessionsLikeRust mirrors Rust
// session_start.rs: the embedded resume/fork handlers surface the app server's
// archived guidance so the TUI can offer to unarchive, and the unarchive action
// clears the gate for the retried resume.
func TestInteractiveResumeAndForkRejectArchivedSessionsLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	id := session.ThreadID("019e72f4-e09a-70f2-b2c2-a153a57b8cc0")
	store := newSessionStore()
	if err := store.Create(&session.Record{ID: id, SessionID: "s-archived", Title: "archived", Metadata: session.Metadata{HistoryMode: "legacy"}}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Archive(id); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	want := archivedSessionStartError(id).Error()
	for name, handler := range map[string]func(codextui.SessionSelection) error{
		"resume": func(selection codextui.SessionSelection) error {
			_, err := interactiveResumeSessionHandler(nil)(selection)
			return err
		},
		"fork": func(selection codextui.SessionSelection) error {
			_, err := interactiveSessionActionHandler(nil)(selection)
			return err
		},
	} {
		selection := codextui.SessionSelection{
			Kind:   codextui.SessionSelectionResume,
			Target: codextui.SessionTarget{ThreadID: string(id)},
		}
		if name == "fork" {
			selection.Kind = codextui.SessionSelectionFork
		}
		err := handler(selection)
		if err == nil || err.Error() != want {
			t.Fatalf("%s archived error = %v, want %q", name, err, want)
		}
	}
	if _, err := interactiveSessionActionHandler(nil)(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionUnarchive,
		Target: codextui.SessionTarget{ThreadID: string(id)},
	}); err != nil {
		t.Fatalf("unarchive error = %v", err)
	}
	if _, err := interactiveResumeSessionHandler(nil)(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{ThreadID: string(id)},
	}); err != nil {
		t.Fatalf("resume after unarchive error = %v", err)
	}
}
