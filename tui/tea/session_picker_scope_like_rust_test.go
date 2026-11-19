package tea

import (
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// TestSessionPickerListingsUseFilterScopeLikeRust mirrors Rust's
// resume_picker PageCwdFilter: the Cwd filter lists the requested directory
// alone, Repo lists the expanded checkout set, and All lists every directory.
func TestSessionPickerListingsUseFilterScopeLikeRust(t *testing.T) {
	var scopes []codextui.SessionPickerScope
	model := NewModel(codextui.NewState(nil), Options{
		Width:             80,
		Height:            24,
		DisablePasteBurst: true,
		SessionPickerCWD:  "/repo",
		SessionPickerItems: []codextui.SessionSummary{
			{ThreadID: "thread-1", Preview: "one", CWD: "/repo"},
		},
		OnListSessionPickerItems: func(scope codextui.SessionPickerScope, cursor string) ([]codextui.SessionSummary, string, string, error) {
			scopes = append(scopes, scope)
			return []codextui.SessionSummary{
				{ThreadID: "thread-1", Preview: "one", CWD: "/repo"},
				{ThreadID: "thread-2", Preview: "linked", CWD: "/repo-wt"},
			}, "", "", nil
		},
	})
	typeText(t, model, "/resume")
	_, cmd := model.Update(key(keymsg.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatalf("resume picker not open: %#v", model.modal)
	}
	if len(scopes) != 1 || scopes[0].All || len(scopes[0].CWDs) != 1 || scopes[0].CWDs[0] != "/repo" {
		t.Fatalf("first listing scope = %#v, want the requested directory", scopes)
	}

	_, cmd = model.Update(key(keymsg.KeyRight))
	runTeaCmd(t, model, cmd)
	if len(scopes) != 2 || !scopes[1].All {
		t.Fatalf("All filter scope = %#v, want every directory", scopes)
	}

	_, cmd = model.Update(SessionPickerRepoFilterMsg{
		PickerID:  model.modal.id,
		Available: true,
		CWDs:      []string{"/repo", "/repo-wt"},
	})
	runTeaCmd(t, model, cmd)
	if !model.modal.sessionPicker.RepoFilterAvailable {
		t.Fatalf("Repo filter unavailable after discovery")
	}

	_, cmd = model.Update(key(keymsg.KeyLeft))
	runTeaCmd(t, model, cmd)
	if len(scopes) != 3 || scopes[2].All || len(scopes[2].CWDs) != 2 {
		t.Fatalf("Repo filter scope = %#v, want the expanded checkout set", scopes)
	}
}
