package tea

import (
	"fmt"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func pinnedPromptModel(t *testing.T, width int) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	for i := 0; i < 12; i++ {
		state.AddMessage(codextui.RoleUser, fmt.Sprintf("prompt-%d", i))
		state.AddMessage(codextui.RoleAssistant, fmt.Sprintf("answer-%d line one\nline two\nline three", i))
	}
	model := NewModel(state, Options{Width: width, Height: 14})
	model.refreshTranscript()
	return model
}

// Rust #52778: a scrolled-away prompt is pinned above the transcript and a
// click jumps back to the original message.
func TestPinnedPromptHeaderJumpsToOriginalPromptLikeRust(t *testing.T) {
	model := pinnedPromptModel(t, 60)
	_ = model.View()
	model.scrollTranscriptRows(-24)

	view := utils.StripANSI(model.View())
	if !model.pinnedPrompt.active {
		t.Fatalf("pinned prompt did not activate:\n%s", view)
	}
	lines := strings.Split(view, "\n")
	if model.pinnedPrompt.row >= len(lines) || !strings.HasPrefix(strings.TrimSpace(lines[model.pinnedPrompt.row]), "\u203a prompt-") {
		t.Fatalf("pinned row = %d lines=%#v", model.pinnedPrompt.row, lines)
	}
	header := strings.TrimSpace(lines[model.pinnedPrompt.row])
	model.Update(bubbletea.MouseMsg{
		X:      2,
		Y:      model.pinnedPrompt.row,
		Action: bubbletea.MouseActionPress,
		Button: bubbletea.MouseButtonLeft,
	})
	if view := utils.StripANSI(model.View()); !strings.Contains(view, strings.TrimPrefix(header, "\u203a ")) {
		t.Fatalf("click did not reveal the prompt:\n%s", view)
	}
}

// Rust #52778: the header yields to visible prompts and narrow screens.
func TestPinnedPromptHeaderVisibilityLikeRust(t *testing.T) {
	model := pinnedPromptModel(t, 60)
	_ = model.View()
	model.scrollTranscriptRows(-1000)
	_ = model.View()
	if model.pinnedPrompt.active {
		t.Fatal("header pinned while the prompt itself is visible")
	}

	narrow := pinnedPromptModel(t, 15)
	_ = narrow.View()
	narrow.scrollTranscriptRows(-4)
	_ = narrow.View()
	if narrow.pinnedPrompt.active {
		t.Fatal("narrow screen pinned a prompt header")
	}
}
