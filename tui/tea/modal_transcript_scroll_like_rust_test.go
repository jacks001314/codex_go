package tea

import (
	"fmt"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	mentionsv2 "codex_go/tui/bottom_pane/mentions_v2"
)

func newScrollableTranscriptModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	for i := 0; i < 40; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("event %02d\nmore detail", i))
	}
	model := NewModel(state, Options{Width: 60, Height: 10})
	if model.TranscriptYOffset() <= 0 {
		t.Fatalf("fixture transcript offset = %d, want a scrollable bottom", model.TranscriptYOffset())
	}
	return model
}

func wheelUp() bubbletea.MouseMsg {
	return bubbletea.MouseMsg{Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonWheelUp}
}

// TestTranscriptWheelScrollsWhileModalOpenLikeRust pins Rust #48805
// (d9487a2930, codex-rs/tui/src/app/owned_transcript.rs
// ::handle_owned_transcript_event; the Rust test is
// plan_menu_allows_transcript_wheel_scrolling_and_keeps_keyboard_ownership):
// a modal keeps the keyboard, but the wheel still moves the visible transcript,
// so a long plan can be reviewed while the prompt is open.
func TestTranscriptWheelScrollsWhileModalOpenLikeRust(t *testing.T) {
	model := newScrollableTranscriptModel(t)
	model.modal = &modalState{
		kind:    ModalKindApproval,
		id:      "plan",
		title:   "Implement this plan?",
		options: []ModalOption{{ID: "yes", Label: "Yes, implement this plan", Shortcut: "1"}},
	}
	before := model.TranscriptYOffset()
	model.Update(wheelUp())
	if got := model.TranscriptYOffset(); got != before-1 {
		t.Fatalf("wheel offset with a modal open = %d, want %d", got, before-1)
	}
	// Keyboard ownership stays with the modal: an unrelated rune never reaches
	// the composer.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("z")})
	if got := model.composer.Value(); got != "" {
		t.Fatalf("composer = %q, want the modal to keep the keyboard", got)
	}
	if model.modal == nil {
		t.Fatal("an unrelated rune key closed the modal")
	}
}

// TestTranscriptWheelStaysBlockedByCompletionPopupLikeRust pins the restriction
// Rust #48805 preserves: a completion popup owns every gesture, so the wheel
// must not move the transcript behind it.
func TestTranscriptWheelStaysBlockedByCompletionPopupLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(model *Model)
	}{
		{name: "slash", open: func(model *Model) { model.slashPopup.Active = true }},
		{name: "skill", open: func(model *Model) { model.skillPopup.Active = true }},
		{name: "mention", open: func(model *Model) { model.mentionPopup = &mentionsv2.Popup{} }},
		{name: "none", open: func(model *Model) {}},
	} {
		model := newScrollableTranscriptModel(t)
		tc.open(model)
		before := model.TranscriptYOffset()
		model.Update(wheelUp())
		got := model.TranscriptYOffset()
		if tc.name == "none" {
			if got != before-1 {
				t.Fatalf("wheel offset without a popup = %d, want %d", got, before-1)
			}
			continue
		}
		if got != before {
			t.Fatalf("wheel offset with the %s popup open = %d, want %d (the popup owns the gesture)", tc.name, got, before)
		}
	}
}
