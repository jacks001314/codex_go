package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

func insetTestModel(t *testing.T) *Model {
	t.Helper()
	animations := false
	model := NewModel(codextui.NewState(nil), Options{Width: 60, Height: 20, AnimationsEnabled: &animations})
	model.State.ThreadID = "root"
	model.State.Messages = []codextui.Message{{
		Role: codextui.RoleUser,
		Text: "A deliberately long message that must wrap inside the inset width instead of the full viewport.",
	}}
	model.refreshTranscript()
	return model
}

func TestConversationInsetPadsToRectLikeRust(t *testing.T) {
	model := insetTestModel(t)
	inset := ConversationRect{X: 4, Y: 2, Width: 40, Height: 12}
	model.conversationInset = &inset

	view := model.viewString()
	lines := strings.Split(view, "\n")
	if len(lines) != model.height {
		t.Fatalf("rendered rows = %d, want %d", len(lines), model.height)
	}
	for row := 0; row < inset.Y; row++ {
		if strings.TrimSpace(lines[row]) != "" {
			t.Fatalf("row %d outside the inset carries content: %q", row, lines[row])
		}
	}
	for index, line := range lines {
		if width := codextui.DisplayWidth(line); width != model.width {
			t.Fatalf("row %d width = %d, want %d: %q", index, width, model.width, line)
		}
	}
	for row := inset.Y; row < inset.Y+inset.Height; row++ {
		if !strings.HasPrefix(lines[row], strings.Repeat(" ", inset.X)) {
			t.Fatalf("row %d inside the inset is not offset: %q", row, lines[row])
		}
		if content := strings.TrimLeft(lines[row], " "); content == "" {
			continue
		}
		if width := codextui.DisplayWidth(strings.TrimRight(lines[row], " ")); width > inset.X+inset.Width {
			t.Fatalf("row %d overflows the inset: %q", row, lines[row])
		}
	}
	for row := inset.Y + inset.Height; row < model.height; row++ {
		if strings.TrimSpace(lines[row]) != "" {
			t.Fatalf("row %d below the inset carries content: %q", row, lines[row])
		}
	}
}

func TestConversationInsetReflowsAndRestoresLayoutLikeRust(t *testing.T) {
	full := insetTestModel(t)
	fullView := full.View()

	model := insetTestModel(t)
	fullWidth := model.width
	inset := ConversationRect{X: 0, Y: 3, Width: 24, Height: 10}
	model.conversationInset = &inset

	view := model.viewString()
	if model.width != fullWidth {
		t.Fatalf("layout width after the inset render = %d, want %d", model.width, fullWidth)
	}
	if model.transcript.Width != fullWidth {
		t.Fatalf("transcript width after the inset render = %d, want %d", model.transcript.Width, fullWidth)
	}
	if model.composerTopRow < inset.Y || model.composerTopRow >= inset.Y+inset.Height {
		t.Fatalf("composer row = %d, want it inside rows %d..%d", model.composerTopRow, inset.Y, inset.Y+inset.Height)
	}
	if model.transcriptContentTopRow != inset.Y {
		t.Fatalf("transcript top row = %d, want the inset origin %d", model.transcriptContentTopRow, inset.Y)
	}
	if got, want := len(messageFragmentRows(view)), len(messageFragmentRows(fullView.Content)); got <= want {
		t.Fatalf("inset transcript rows = %d, want more than the %d full-width rows: the transcript did not reflow", got, want)
	}
}

const insetTestMessage = "A deliberately long message that must wrap inside the inset width instead of the full viewport."

// messageFragmentRows counts the painted rows that carry part of the test
// message, so a narrower inset must produce more of them.
func messageFragmentRows(view string) []string {
	rows := []string{}
	for _, line := range strings.Split(view, "\n") {
		text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ansi.Strip(line)), "\u203a"))
		if text != "" && strings.Contains(insetTestMessage, text) {
			rows = append(rows, text)
		}
	}
	return rows
}

func TestConversationInsetLeavesCenteredDialogFullScreenLikeRust(t *testing.T) {
	model := insetTestModel(t)
	inset := ConversationRect{X: 4, Y: 2, Width: 40, Height: 12}
	model.conversationInset = &inset
	model.openModal(ModalRequestMsg{
		ID:      "confirm",
		Kind:    ModalKindGeneric,
		Title:   "Delete thread?",
		Options: []ModalOption{{ID: "yes", Label: "Delete"}, {ID: "no", Label: "Cancel"}},
	})

	view := model.viewString()
	lines := strings.Split(view, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "" || strings.TrimSpace(lines[1]) != "" {
		t.Fatalf("centered dialog was painted inside the inset: %q", strings.Join(lines[:2], "|"))
	}
	if !strings.Contains(view, "Delete thread?") {
		t.Fatalf("centered dialog content missing: %q", view)
	}
}
