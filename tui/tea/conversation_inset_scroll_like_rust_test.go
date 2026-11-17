package tea

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	mousemsg "codex_go/tui/mousemsg"
)

// Rust #58718 keeps the transcript scroll position when an input-triggered
// redraw re-prepares the conversation inside the inset, and the empty-state /
// follow behaviour must not snap back to the bottom.
func TestConversationInsetPreservesTranscriptScrollLikeRust(t *testing.T) {
	animations := false
	model := NewModel(codextui.NewState(nil), Options{Width: 60, Height: 20, AnimationsEnabled: &animations})
	model.State.ThreadID = "root"
	messages := make([]codextui.Message, 0, 40)
	for index := 0; index < 40; index++ {
		messages = append(messages, codextui.Message{
			Role: codextui.RoleUser,
			Text: fmt.Sprintf("message %d with padding text so the transcript overflows the inset", index),
		})
	}
	model.State.Messages = messages
	model.refreshTranscript()
	inset := ConversationRect{X: 4, Y: 3, Width: 30, Height: 12}
	model.conversationInset = &inset
	_ = model.viewString()

	model.activityFollow = false
	model.transcript.SetYOffset(5)
	before := model.transcript.YOffset
	if before != 5 {
		t.Fatalf("initial scroll offset = %d, want 5", before)
	}
	_ = model.viewString()
	if got := model.transcript.YOffset; got != before {
		t.Fatalf("inset redraw moved the scroll offset %d -> %d", before, got)
	}
	if model.transcript.maxYOffset() <= 0 {
		t.Fatal("test transcript does not overflow the inset")
	}
	view := ansi.Strip(model.viewString())
	if strings.Contains(view, "message 0 ") {
		t.Fatalf("scrolled transcript painted the first message: %q", view)
	}
}

// Rust #58718 translates the composer's text origin by the inset offset so a
// pointer drag selects the character under the cursor, not one shifted by the
// rectangle origin.
func TestConversationInsetComposerClickSelectsPaintedTextLikeRust(t *testing.T) {
	animations := false
	model := NewModel(codextui.NewState(nil), Options{
		Width:               60,
		Height:              20,
		AnimationsEnabled:   &animations,
		AlternateScreenMode: "always",
		CopyOnSelect:        "never",
	})
	model.State.ThreadID = "root"
	model.State.Messages = []codextui.Message{{Role: codextui.RoleUser, Text: "hi"}}
	model.refreshTranscript()
	inset := ConversationRect{X: 4, Y: 3, Width: 30, Height: 12}
	model.conversationInset = &inset
	model.composer.SetValue("hello world")

	view := ansi.Strip(model.viewString())
	lines := strings.Split(view, "\n")
	row := -1
	column := -1
	for index, line := range lines {
		if at := strings.Index(line, "hello world"); at >= 0 {
			row = index
			column = codextui.DisplayWidth(line[:at])
			break
		}
	}
	if row < 0 {
		t.Fatalf("composer draft missing from the inset view: %q", view)
	}
	if row < inset.Y || row >= inset.Y+inset.Height {
		t.Fatalf("composer draft row %d escaped the inset rows %d..%d", row, inset.Y, inset.Y+inset.Height)
	}
	if column < inset.X {
		t.Fatalf("composer draft column %d escaped the inset origin %d", column, inset.X)
	}

	model.Update(mousemsg.MouseMsg{
		X: column, Y: row, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
	})
	model.Update(mousemsg.MouseMsg{
		X: column + codextui.DisplayWidth("hello"), Y: row, Action: mousemsg.MouseActionRelease, Button: mousemsg.MouseButtonLeft,
	})
	if got := model.composerSelectedText(); got != "hello" {
		t.Fatalf("composer selection = %q, want hello", got)
	}
}

// Rust #58718 keeps centered dialogs relative to the full viewport and restores
// the inset conversation when they close.
func TestConversationInsetRestoresAfterDialogDismissalLikeRust(t *testing.T) {
	model := insetTestModel(t)
	inset := ConversationRect{X: 4, Y: 2, Width: 40, Height: 12}
	model.conversationInset = &inset
	_ = model.viewString()

	model.openModal(ModalRequestMsg{
		ID:      "confirm",
		Kind:    ModalKindGeneric,
		Title:   "Delete thread?",
		Options: []ModalOption{{ID: "yes", Label: "Delete"}, {ID: "no", Label: "Cancel"}},
	})
	if view := model.viewString(); !strings.Contains(view, "Delete thread?") {
		t.Fatalf("centered dialog content missing: %q", view)
	}
	model.modal = nil

	view := ansi.Strip(model.viewString())
	lines := strings.Split(view, "\n")
	for row := 0; row < inset.Y && row < len(lines); row++ {
		if strings.TrimSpace(lines[row]) != "" {
			t.Fatalf("row %d outside the inset still carries dialog content after dismissal: %q", row, lines[row])
		}
	}
	if strings.Contains(view, "Delete thread?") {
		t.Fatalf("dialog content survived dismissal: %q", view)
	}
}
