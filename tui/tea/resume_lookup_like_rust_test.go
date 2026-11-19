package tea

import (
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust resolves `/resume <id-or-name>` through the backend
// (AppEvent::ResumeSessionByIdOrName -> lookup_session_target_with_app_server),
// so the command must work before any picker snapshot was loaded.
func TestResumeByIdOrNameListsFromBackendLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	var responses []ModalResponse
	listed := 0
	model := NewModel(state, Options{
		Width:             80,
		Height:            24,
		DisablePasteBurst: true,
		OnListSessionPickerItems: func(scope codextui.SessionPickerScope, cursor string) ([]codextui.SessionSummary, string, string, error) {
			listed++
			return []codextui.SessionSummary{{
				ThreadID: "thread-auth",
				Title:    "Investigate auth flow",
			}}, "", "", nil
		},
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	typeText(t, model, "/resume Investigate auth flow")
	_, cmd := model.Update(key(keymsg.KeyEnter))
	runTeaCmd(t, model, cmd)
	// Rust's named_session_lookup walks the active then the archived
	// collection (named_session_lookup.rs), so both are listed.
	if listed != 2 {
		t.Fatalf("backend listings = %d, want 2", listed)
	}
	if state.ThreadID != "thread-auth" {
		t.Fatalf("resumed thread = %q, want thread-auth", state.ThreadID)
	}
	if len(model.sessionItems) != 1 || model.sessionItems[0].ThreadID != "thread-auth" {
		t.Fatalf("session cache = %#v, want the backend listing merged in", model.sessionItems)
	}
	if len(responses) == 0 || responses[len(responses)-1].Picker == nil || responses[len(responses)-1].Picker.Value != "thread-auth" {
		t.Fatalf("resume response missing: %#v", responses)
	}
}
