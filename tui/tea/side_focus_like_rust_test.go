package tea

import (
	"testing"

	codextui "codex_go/tui"
)

// Rust #59130/#59208/#59287 install these five workspace pane actions as
// implicit leader chords. They stay out of the static catalog defaults so an
// explicit user binding can claim the chord without a false conflict.
func TestWorkspacePaneLeaderBindingsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 30})
	actions, prefixes, ok := model.leaderBindingsFor("ctrl-x", nil)
	if !ok || len(prefixes) == 0 {
		t.Fatal("default leader prefix is missing")
	}
	want := map[string]string{
		"c":     "global.focus_main_composer",
		"s":     "global.focus_sidebar_content",
		"-":     "global.hide_pane",
		"left":  "global.focus_pane_left",
		"h":     "global.focus_pane_left",
		"right": "global.focus_pane_right",
		"l":     "global.focus_pane_right",
	}
	for completion, action := range want {
		if actions[completion] != action {
			t.Fatalf("leader %s = %q, want %q (bindings=%#v)", completion, actions[completion], action, actions)
		}
	}
	for _, action := range []string{
		"global.focus_main_composer",
		"global.focus_sidebar_content",
		"global.focus_pane_left",
		"global.focus_pane_right",
		"global.hide_pane",
	} {
		if _, ok := codextui.FindKeymapAction("global", action[len("global."):]); !ok {
			t.Fatalf("%s is missing from the keymap catalog", action)
		}
	}
}

func newWorkspacePaneModel(t *testing.T) (*Model, *activeSideConversation) {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("side")
	model := NewModel(state, Options{Width: 120, Height: 30})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	side := &activeSideConversation{
		ParentThreadID: "parent",
		SideThreadID:   "side",
		ShowingSide:    true,
	}
	model.activeSide = side
	model.retainSideConversation(side)
	return model, side
}

func TestWorkspacePaneFocusAndHideLikeRust(t *testing.T) {
	model, _ := newWorkspacePaneModel(t)
	if model.State.ThreadID != "side" {
		t.Fatalf("initial thread = %q, want the focused side pane", model.State.ThreadID)
	}

	model.focusMainComposer()
	if model.State.ThreadID != "parent" || model.activeSide == nil || model.activeSide.ShowingSide {
		t.Fatalf("focus_main_composer left thread=%q side=%#v", model.State.ThreadID, model.activeSide)
	}

	model.focusSidebarContent()
	if model.State.ThreadID != "side" || model.activeSide == nil || !model.activeSide.ShowingSide {
		t.Fatalf("focus_sidebar_content left thread=%q side=%#v", model.State.ThreadID, model.activeSide)
	}

	model.hideFocusedSidePane()
	if model.activeSide != nil {
		t.Fatal("hide_pane left the pane visible")
	}
	if model.State.ThreadID != "parent" {
		t.Fatalf("hide_pane left thread = %q, want parent", model.State.ThreadID)
	}
	if retained := model.retainedSideConversationForParent("parent"); retained == nil {
		t.Fatal("hide_pane discarded the retained side conversation")
	}

	model.focusSidebarContent()
	if model.activeSide == nil || !model.activeSide.ShowingSide || model.State.ThreadID != "side" {
		t.Fatalf("focus_sidebar_content did not reopen the hidden pane: thread=%q side=%#v", model.State.ThreadID, model.activeSide)
	}
}
