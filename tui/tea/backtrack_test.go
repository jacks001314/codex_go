package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
)

func backtrackTestModel(t *testing.T, onPromptEdit PromptEditFunc, messages ...codextui.Message) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	for _, message := range messages {
		if message.Text == "" {
			continue
		}
		state.AddMessage(message.Role, message.Text)
	}
	return NewModel(state, Options{Width: 80, Height: 24, OnPromptEdit: onPromptEdit})
}

func backtrackTestMessages() []codextui.Message {
	return []codextui.Message{
		{Role: codextui.RoleUser, Text: "first"},
		{Role: codextui.RoleAssistant, Text: "one"},
		{Role: codextui.RoleUser, Text: "second"},
		{Role: codextui.RoleAssistant, Text: "two"},
	}
}

func TestModelBacktrackEscPrimesThenOpensHighlightedPreview(t *testing.T) {
	model := backtrackTestModel(t, nil, backtrackTestMessages()...)

	model.Update(key(bubbletea.KeyEsc))
	if !model.backtrack.Primed || !model.escBacktrackHint {
		t.Fatalf("first Esc should prime backtracking: %#v hint=%v", model.backtrack, model.escBacktrackHint)
	}
	if model.overlay != nil {
		t.Fatal("the first Esc must not open the overlay")
	}
	if view := model.View(); !strings.Contains(view, "Esc again to edit previous message") {
		t.Fatalf("primed footer missing the edit hint:\n%s", view)
	}

	model.Update(key(bubbletea.KeyEsc))
	if model.overlay == nil || !model.overlayTranscript {
		t.Fatal("the second Esc should open the transcript overlay")
	}
	if !model.backtrack.OverlayPreviewActive {
		t.Fatal("the overlay should be in backtrack preview mode")
	}
	if model.backtrack.NthUserMessage != 1 {
		t.Fatalf("preview should select the newest user message, got %d", model.backtrack.NthUserMessage)
	}
	if _, _, ok := model.overlay.HighlightRange(); !ok {
		t.Fatal("the newest user message should be highlighted")
	}
	if model.escBacktrackHint {
		t.Fatal("the composer hint should clear once the overlay is open")
	}
}

func TestModelBacktrackOverlayStepsAndConfirmsFork(t *testing.T) {
	var selection tuiapp.PromptEditSelection
	calls := 0
	onPromptEdit := func(prompt tuiapp.PromptEditSelection) (SessionResumeResponse, error) {
		calls++
		selection = prompt
		return SessionResumeResponse{Summary: &codextui.SessionSummary{ThreadID: "thread-forked"}}, nil
	}
	model := backtrackTestModel(t, onPromptEdit, backtrackTestMessages()...)

	model.Update(key(bubbletea.KeyEsc))
	model.Update(key(bubbletea.KeyEsc))
	if model.backtrack.NthUserMessage != 1 {
		t.Fatalf("preview ordinal = %d, want 1", model.backtrack.NthUserMessage)
	}

	// Left steps to the older prompt, Right steps back to the newest.
	model.Update(key(bubbletea.KeyLeft))
	if model.backtrack.NthUserMessage != 0 {
		t.Fatalf("Left ordinal = %d, want 0", model.backtrack.NthUserMessage)
	}
	model.Update(key(bubbletea.KeyRight))
	if model.backtrack.NthUserMessage != 1 {
		t.Fatalf("Right ordinal = %d, want 1", model.backtrack.NthUserMessage)
	}

	// At the oldest prompt moving further back stays put.
	model.Update(key(bubbletea.KeyLeft))
	model.Update(key(bubbletea.KeyLeft))
	if model.backtrack.NthUserMessage != 0 {
		t.Fatalf("clamped ordinal = %d, want 0", model.backtrack.NthUserMessage)
	}
	model.Update(key(bubbletea.KeyRight))

	model.Update(key(bubbletea.KeyEnter))
	if calls != 1 {
		t.Fatalf("prompt-edit calls = %d, want 1", calls)
	}
	if selection.ThreadID != "thread-1" || selection.UserOrdinal != 1 || selection.Prompt.Text != "second" {
		t.Fatalf("selection = %#v", selection)
	}
	if model.overlay != nil {
		t.Fatal("confirming should close the transcript overlay")
	}
	if model.backtrack.Primed || model.backtrack.OverlayPreviewActive {
		t.Fatalf("confirming should reset backtrack state: %#v", model.backtrack)
	}
	if got := model.composer.Value(); got != "second" {
		t.Fatalf("composer = %q, want the selected prompt", got)
	}
	if model.State.ThreadID != "thread-forked" {
		t.Fatalf("thread = %q, want thread-forked", model.State.ThreadID)
	}
}

func TestModelBacktrackConfirmFromMainRestoresPrompt(t *testing.T) {
	var selection tuiapp.PromptEditSelection
	onPromptEdit := func(prompt tuiapp.PromptEditSelection) (SessionResumeResponse, error) {
		selection = prompt
		return SessionResumeResponse{Summary: &codextui.SessionSummary{ThreadID: "thread-forked"}}, nil
	}
	model := backtrackTestModel(t, onPromptEdit, backtrackTestMessages()...)

	// Prime and select a prompt manually, then confirm from the main view.
	model.backtrack.Prime("thread-1")
	model.backtrack.NthUserMessage = 1
	model.Update(key(bubbletea.KeyEnter))

	if selection.UserOrdinal != 1 || model.composer.Value() != "second" {
		t.Fatalf("selection = %#v composer = %q", selection, model.composer.Value())
	}
}

func TestModelBacktrackFailureRestoresPromptAndReportsError(t *testing.T) {
	onPromptEdit := func(tuiapp.PromptEditSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, assertError("branch unavailable")
	}
	model := backtrackTestModel(t, onPromptEdit, backtrackTestMessages()...)

	model.backtrack.Prime("thread-1")
	model.backtrack.NthUserMessage = 1
	model.Update(key(bubbletea.KeyEnter))

	if model.State.ThreadID != "thread-1" {
		t.Fatalf("a failed branch must keep the source thread, got %q", model.State.ThreadID)
	}
	if model.composer.Value() != "second" {
		t.Fatalf("composer = %q, want the restored prompt", model.composer.Value())
	}
	if text := modelMessageText(model); !strings.Contains(text, "Failed to branch before the selected prompt: branch unavailable") {
		t.Fatalf("missing branch failure message:\n%s", text)
	}
}

func TestModelBacktrackWithoutTargetShowsInfoMessage(t *testing.T) {
	model := backtrackTestModel(t, nil, codextui.Message{Role: codextui.RoleAssistant, Text: "one"})

	model.Update(key(bubbletea.KeyEsc))
	if !model.backtrack.Primed || model.escBacktrackHint {
		t.Fatalf("priming without a target should not advertise the hint: %#v", model.backtrack)
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.overlay != nil || model.backtrack.Primed {
		t.Fatal("priming without a target should reset and not open the overlay")
	}
	if text := modelMessageText(model); !strings.Contains(text, "No previous message to edit.") {
		t.Fatalf("missing no-previous-message notice:\n%s", text)
	}
}

func TestModelBacktrackTypingCancelsPrimedState(t *testing.T) {
	model := backtrackTestModel(t, nil, backtrackTestMessages()...)

	model.Update(key(bubbletea.KeyEsc))
	if !model.backtrack.Primed {
		t.Fatal("expected the first Esc to prime")
	}
	typeText(t, model, "x")
	if model.backtrack.Primed || model.escBacktrackHint {
		t.Fatalf("typing should cancel a primed backtrack: %#v", model.backtrack)
	}
}

func TestModelBacktrackSideConversationRejected(t *testing.T) {
	model := backtrackTestModel(t, nil, backtrackTestMessages()...)
	model.activeSide = &activeSideConversation{ShowingSide: true}

	model.Update(key(bubbletea.KeyEsc))
	if model.backtrack.Primed || model.overlay != nil {
		t.Fatal("side conversations must not prime backtracking")
	}
	if text := modelMessageText(model); !strings.Contains(text, "Editing previous prompts is unavailable in side conversations.") {
		t.Fatalf("missing side-conversation notice:\n%s", text)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
