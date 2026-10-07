package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// Rust #48549: /copy must hand the completed response to the clipboard with its
// Markdown line endings intact. This drives the real keypress path
// (/copy -> picker -> Enter) on a response that ends in a hard break.
func TestSlashCopyPreservesCompletedResponseTrailingHardBreak(t *testing.T) {
	const markdown = "Intro\n\n```text\ncode with trailing spaces  \n```\n\nlast line  "
	state := codextui.NewState(nil)
	var copied string
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnClipboardWrite: func(text string) error {
			copied = text
			return nil
		},
	})
	// The turn completes through the production assistant-final path.
	model.mergeAssistantFinal(markdown)

	typeText(t, model, "/copy")
	model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(model.View(), "Copy to clipboard") {
		t.Fatalf("/copy should open a target picker:\n%s", model.View())
	}
	model.Update(key(bubbletea.KeyEnter))

	if copied != markdown {
		t.Fatalf("copied = %q, want %q", copied, markdown)
	}
}

// The completed-message merge keeps the trailing hard break too, so the copy
// source still has it when /copy runs right after a turn completes.
func TestSlashCopyPreservesHardBreakAfterAssistantMessageMerge(t *testing.T) {
	const markdown = "Intro  \nlast line  \n"
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
	})

	model.mergeAssistantFinal(markdown)
	model.mergeAssistantFinal(markdown)
	if len(state.Messages) != 1 {
		t.Fatalf("repeated completion duplicated the response: %#v", state.Messages)
	}
	if got := state.Messages[len(state.Messages)-1].Text; got != "Intro  \nlast line  " {
		t.Fatalf("merged assistant message = %q, want hard breaks preserved", got)
	}

	var copied string
	model.clipboardWrite = func(text string) error {
		copied = text
		return nil
	}
	typeText(t, model, "/copy")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyEnter))

	if copied != "Intro  \nlast line  " {
		t.Fatalf("copied = %q, want %q", copied, "Intro  \nlast line  ")
	}
}

// The duplicate guard compares the same normalized body, so a response that
// keeps its trailing hard break is still recognized as already present.
func TestAssistantFinalExistsMatchesNormalizedHardBreakBody(t *testing.T) {
	messages := []codextui.Message{
		{Role: codextui.RoleAssistant, Text: "answer  "},
		{Role: codextui.RoleHistory, Text: "note"},
	}
	if !assistantFinalExistsInCurrentTurn(messages, "answer  ") {
		t.Fatal("a completed response with a trailing hard break should be recognized as present")
	}
}
