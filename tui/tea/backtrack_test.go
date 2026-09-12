package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	bottompane "codex_go/tui/bottom_pane"
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

func TestModelBacktrackRestoresPromptInsteadOfRenderedAttachmentListing(t *testing.T) {
	var selection tuiapp.PromptEditSelection
	onPromptEdit := func(prompt tuiapp.PromptEditSelection) (SessionResumeResponse, error) {
		selection = prompt
		return SessionResumeResponse{Summary: &codextui.SessionSummary{ThreadID: "thread-forked"}}, nil
	}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.AddUserPromptMessage(
		"describe\n\nAttachments:\n- image: /tmp/chart.png",
		"describe",
		[]string{"/tmp/chart.png"},
		[]string{"https://example.test/remote.png"},
		[]codextui.MessageTextElement{{Start: 0, End: 8, Placeholder: "describe"}},
	)
	model := NewModel(state, Options{Width: 80, Height: 24, OnPromptEdit: onPromptEdit})
	model.backtrack.Prime("thread-1")
	model.backtrack.NthUserMessage = 0

	model.Update(key(bubbletea.KeyEnter))

	if selection.Prompt.Text != "describe" || len(selection.Prompt.LocalImages) != 1 {
		t.Fatalf("selection prompt = %#v", selection.Prompt)
	}
	if got := model.composer.Value(); got != "describe" {
		t.Fatalf("composer = %q, want the original prompt (no attachment listing)", got)
	}
	if len(model.attachments) != 2 {
		t.Fatalf("attachments = %#v, want the local and remote image", model.attachments)
	}
	if model.attachments[0].Kind != bottompane.AttachmentImage || model.attachments[0].Path != "/tmp/chart.png" {
		t.Fatalf("local attachment = %#v", model.attachments[0])
	}
	if model.attachments[1].Kind != bottompane.AttachmentRemoteImage || model.attachments[1].URL != "https://example.test/remote.png" {
		t.Fatalf("remote attachment = %#v", model.attachments[1])
	}
	if len(model.composerElements) != 1 || model.composerElements[0].Placeholder != "describe" {
		t.Fatalf("composer elements = %#v", model.composerElements)
	}
}

func TestModelSubmitRecordsRestorablePromptState(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:             80,
		Height:            24,
		DisablePasteBurst: true,
		OnSubmitRequest:   func(SubmitRequest) bubbletea.Cmd { return nil },
	})
	model.attachments = []bottompane.ComposerAttachment{
		{Kind: bottompane.AttachmentImage, Path: "/tmp/chart.png"},
		{Kind: bottompane.AttachmentRemoteImage, URL: "https://example.test/r.png"},
	}
	typeText(t, model, "describe")
	model.Update(key(bubbletea.KeyEnter))

	var user *codextui.Message
	for index := range model.State.Messages {
		if model.State.Messages[index].Role == codextui.RoleUser {
			user = &model.State.Messages[index]
		}
	}
	if user == nil {
		t.Fatal("submitting should append a user message")
	}
	// The rendered entry keeps the attachment listing for display.
	if !strings.Contains(user.Text, "Attachments:") {
		t.Fatalf("rendered user message = %q, want the attachment listing", user.Text)
	}
	// The restorable prompt is the submitted text and its attachments.
	if user.UserPrompt != "describe" {
		t.Fatalf("UserPrompt = %q, want describe", user.UserPrompt)
	}
	if len(user.UserPromptLocalImages) != 1 || user.UserPromptLocalImages[0] != "/tmp/chart.png" {
		t.Fatalf("local images = %#v", user.UserPromptLocalImages)
	}
	if len(user.UserPromptRemoteImages) != 1 || user.UserPromptRemoteImages[0] != "https://example.test/r.png" {
		t.Fatalf("remote images = %#v", user.UserPromptRemoteImages)
	}
}

func TestUserPromptMessageStateCollectsAttachmentsAndElements(t *testing.T) {
	request := SubmitRequest{
		Prompt: "  hello  ",
		Attachments: []bottompane.ComposerAttachment{
			{Kind: bottompane.AttachmentImage, Path: "/tmp/a.png"},
			{Kind: bottompane.AttachmentImage},
			{Kind: bottompane.AttachmentRemoteImage, URL: "https://example.test/b.png"},
		},
		TextElements: []ComposerTextElement{{Start: 2, End: 7, Placeholder: "hello"}},
	}
	text, local, remote, elements := userPromptMessageState(request)
	if text != "hello" {
		t.Fatalf("text = %q, want the trimmed prompt", text)
	}
	if len(local) != 1 || local[0] != "/tmp/a.png" {
		t.Fatalf("local = %#v", local)
	}
	if len(remote) != 1 || remote[0] != "https://example.test/b.png" {
		t.Fatalf("remote = %#v", remote)
	}
	if len(elements) != 1 || elements[0].Start != 2 || elements[0].End != 7 || elements[0].Placeholder != "hello" {
		t.Fatalf("elements = %#v", elements)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
