package tea

import (
	"errors"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/internal/protocol"
	codextui "codex_go/internal/tui"
	bottompane "codex_go/internal/tui/bottom_pane"
)

func TestModelViewRendersState(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:          "gpt-test",
		ApprovalPolicy: "on-request",
		Sandbox:        "workspace-write",
	})
	state.SetThreadID("thread-1")
	state.AddMessage(codextui.RoleUser, "hello")
	state.AddMessage(codextui.RoleAssistant, "hi there")

	model := NewModel(state, Options{Width: 72, Height: 18})
	view := model.View()

	for _, want := range []string{"Thread: thread-1", "Model: gpt-test", "User:", "Assistant:", "Enter send"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
}

func TestModelSubmitPrompt(t *testing.T) {
	state := codextui.NewState(nil)
	var submitted []string
	model := NewModel(state, Options{
		OnSubmit: func(prompt string) bubbletea.Cmd {
			submitted = append(submitted, prompt)
			return nil
		},
	})

	typeText(t, model, "hello codex")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd != nil {
		_ = cmd()
	}

	if got := strings.Join(model.SubmittedPrompts(), ","); got != "hello codex" {
		t.Fatalf("SubmittedPrompts = %q, want hello codex", got)
	}
	if got := strings.Join(submitted, ","); got != "hello codex" {
		t.Fatalf("submit callback = %q, want hello codex", got)
	}
	if state.Status != "running" {
		t.Fatalf("Status = %q, want running", state.Status)
	}
	if model.ComposerValue() != "" {
		t.Fatalf("ComposerValue = %q, want empty", model.ComposerValue())
	}
	if !strings.Contains(model.View(), "User:") {
		t.Fatalf("View() should include submitted user message:\n%s", model.View())
	}
}

func TestModelQueuesPromptWhileRunningAndSubmitsAfterCompletion(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	var requests []SubmitRequest
	model := NewModel(state, Options{
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})

	typeText(t, model, "queued while busy")
	model.Update(key(bubbletea.KeyEnter))
	if len(requests) != 0 {
		t.Fatalf("requests before completion = %#v", requests)
	}
	if got := model.QueuedRequests(); len(got) != 1 || got[0].Prompt != "queued while busy" {
		t.Fatalf("queued requests = %#v", got)
	}
	if !strings.Contains(model.View(), "Queued inputs: 1") {
		t.Fatalf("queue line missing:\n%s", model.View())
	}
	if got := model.ComposerValue(); got != "" {
		t.Fatalf("composer after queue = %q, want empty", got)
	}

	model.Update(TurnCompletedMsg{ThreadID: "thread-1", AssistantMessage: "done"})
	if len(requests) != 1 || requests[0].Prompt != "queued while busy" {
		t.Fatalf("requests after completion = %#v", requests)
	}
	if got := model.QueuedRequests(); len(got) != 0 {
		t.Fatalf("queued after completion = %#v", got)
	}
	if state.Status != "running" {
		t.Fatalf("status after queued submit = %q, want running", state.Status)
	}
}

func TestModelTabSubmitsWhenIdleAndQueuesWhenRunning(t *testing.T) {
	var requests []SubmitRequest
	model := NewModel(nil, Options{
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})

	typeText(t, model, "tab submit")
	model.Update(key(bubbletea.KeyTab))
	if len(requests) != 1 || requests[0].Prompt != "tab submit" {
		t.Fatalf("idle tab requests = %#v", requests)
	}

	model.State.SetStatus("running")
	typeText(t, model, "tab queue")
	model.Update(key(bubbletea.KeyTab))
	if len(requests) != 1 {
		t.Fatalf("running tab should not submit immediately: %#v", requests)
	}
	if got := model.QueuedRequests(); len(got) != 1 || got[0].Prompt != "tab queue" {
		t.Fatalf("running tab queued = %#v", got)
	}
}

func TestModelAppliesTurnCompleted(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{})

	model.Update(TurnCompletedMsg{
		ThreadID:         "thread-1",
		AssistantMessage: "done",
	})
	if state.ThreadID != "thread-1" {
		t.Fatalf("ThreadID = %q, want thread-1", state.ThreadID)
	}
	if state.Status != "idle" {
		t.Fatalf("Status = %q, want idle", state.Status)
	}
	if !strings.Contains(model.View(), "Assistant:") || !strings.Contains(model.View(), "done") {
		t.Fatalf("View() missing assistant response:\n%s", model.View())
	}

	model.Update(TurnCompletedMsg{Err: errors.New("boom")})
	if state.Status != "error" {
		t.Fatalf("Status = %q, want error", state.Status)
	}
	if !strings.Contains(model.View(), "Error: boom") {
		t.Fatalf("View() missing error:\n%s", model.View())
	}
}

func TestModelAppliesThreadEvents(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{})

	model.Update(ThreadEventMsg{Event: protocol.ThreadStarted("thread-1")})
	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.ToolCallItem("tool-1", "exec_command", `{"cmd":"date"}`))})
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-1", "hel")})
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-1", "lo")})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.AgentMessageItem("msg-1", "hello"))})
	model.Update(ThreadEventMsg{Event: protocol.TurnCompleted(protocol.Usage{})})

	if state.ThreadID != "thread-1" {
		t.Fatalf("ThreadID = %q, want thread-1", state.ThreadID)
	}
	if state.Status != "idle" {
		t.Fatalf("Status = %q, want idle", state.Status)
	}
	view := model.View()
	for _, want := range []string{"Assistant:", "hello", "Tool started: exec_command", "Turn completed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
	if got := countRole(state.Messages, codextui.RoleAssistant); got != 1 {
		t.Fatalf("assistant message count = %d, want 1; messages=%#v", got, state.Messages)
	}
}

func TestModelConsumesStreamChannel(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{})
	messages := make(chan bubbletea.Msg, 2)
	messages <- ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-1", "streamed")}
	messages <- TurnCompletedMsg{ThreadID: "thread-1", AssistantMessage: "streamed"}
	close(messages)

	_, cmd := model.Update(StreamStartedMsg{Messages: messages})
	if cmd == nil {
		t.Fatal("StreamStartedMsg returned nil command")
	}
	_, cmd = model.Update(cmd())
	if cmd == nil {
		t.Fatal("first stream message returned nil command")
	}
	_, cmd = model.Update(cmd())
	if cmd == nil {
		t.Fatal("second stream message returned nil command")
	}
	_, cmd = model.Update(cmd())
	if cmd != nil {
		t.Fatal("closed stream returned non-nil command")
	}

	if state.ThreadID != "thread-1" {
		t.Fatalf("ThreadID = %q, want thread-1", state.ThreadID)
	}
	if got := countRole(state.Messages, codextui.RoleAssistant); got != 1 {
		t.Fatalf("assistant message count = %d, want 1; messages=%#v", got, state.Messages)
	}
	if !strings.Contains(model.View(), "streamed") {
		t.Fatalf("View() missing streamed text:\n%s", model.View())
	}
}

func TestModelApprovalModalRespondsToShortcut(t *testing.T) {
	state := codextui.NewState(nil)
	var responses []ModalResponse
	model := NewModel(state, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})

	model.Update(ApprovalRequestMsg{
		ID:      "approval-1",
		Title:   "Run command?",
		Command: "go test ./...",
	})
	view := model.View()
	for _, want := range []string{"Run command?", "go test ./...", "Allow for this turn", "Deny"} {
		if !strings.Contains(view, want) {
			t.Fatalf("approval modal missing %q:\n%s", want, view)
		}
	}

	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'d'}}))
	if len(responses) != 1 {
		t.Fatalf("responses len = %d, want 1", len(responses))
	}
	if responses[0].ID != "approval-1" || responses[0].Kind != ModalKindApproval || responses[0].OptionID != "deny" || responses[0].Cancelled {
		t.Fatalf("response = %#v", responses[0])
	}
	if strings.Contains(model.View(), "Run command?") {
		t.Fatalf("modal should be closed:\n%s", model.View())
	}
	if !strings.Contains(model.View(), "Deny") {
		t.Fatalf("notice should include selected label:\n%s", model.View())
	}
}

func TestModelElicitationApprovalRespondsWithDecision(t *testing.T) {
	var responses []ModalResponse
	model := NewModel(nil, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.Update(ElicitationRequestMsg{
		ID:         "elicitation-1",
		ServerName: "docs",
		Message:    "Allow docs search?",
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"decision": map[string]any{
					"type": "string",
					"anyOf": []any{
						map[string]any{"const": "approve_once", "title": "Allow once"},
						map[string]any{"const": "approve_session", "title": "Allow session"},
						map[string]any{"const": "decline", "title": "Decline"},
						map[string]any{"const": "cancel", "title": "Cancel"},
					},
				},
			},
		},
	})
	view := model.View()
	for _, want := range []string{"MCP request from docs", "Allow docs search?", "Allow session", "Decline"} {
		if !strings.Contains(view, want) {
			t.Fatalf("elicitation modal missing %q:\n%s", want, view)
		}
	}
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'a'}}))
	if len(responses) != 1 {
		t.Fatalf("responses len = %d, want 1", len(responses))
	}
	got := responses[0]
	if got.ID != "elicitation-1" || got.Kind != ModalKindElicitation || got.OptionID != "approve_session" || got.Cancelled {
		t.Fatalf("response = %#v", got)
	}
	if got.Elicitation == nil || got.Elicitation.Action != "accept" || got.Elicitation.Persist != "session" {
		t.Fatalf("elicitation decision = %#v", got.Elicitation)
	}
}

func TestModelElicitationFormSubmitsDefaultContent(t *testing.T) {
	var responses []ModalResponse
	model := NewModel(nil, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.Update(ElicitationRequestMsg{
		ID:      "elicitation-2",
		Message: "Configure docs",
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"email": map[string]any{
					"type":    "string",
					"default": "team@example.test",
				},
				"notify": map[string]any{
					"type":    "boolean",
					"default": true,
				},
			},
		},
	})
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'y'}}))
	if len(responses) != 1 {
		t.Fatalf("responses len = %d, want 1", len(responses))
	}
	decision := responses[0].Elicitation
	if decision == nil || decision.Action != "accept" || decision.Content["email"] != "team@example.test" || decision.Content["notify"] != true {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestModelElicitationRequiredFieldStaysOpen(t *testing.T) {
	var responses []ModalResponse
	model := NewModel(nil, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.Update(ElicitationRequestMsg{
		ID:      "elicitation-3",
		Message: "Configure docs",
		RequestedSchema: map[string]any{
			"type":     "object",
			"required": []any{"email"},
			"properties": map[string]any{
				"email": map[string]any{"type": "string"},
			},
		},
	})
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'y'}}))
	if len(responses) != 0 {
		t.Fatalf("responses = %#v, want none while invalid", responses)
	}
	view := model.View()
	if !strings.Contains(view, `field "email" is required`) || !strings.Contains(view, "Configure docs") {
		t.Fatalf("view after invalid submit =\n%s", view)
	}
	model.Update(key(bubbletea.KeyEsc))
	if len(responses) != 1 || responses[0].Elicitation == nil || responses[0].Elicitation.Action != "cancel" {
		t.Fatalf("cancel response = %#v", responses)
	}
}

func TestModelModalNavigationAndCancel(t *testing.T) {
	var responses []ModalResponse
	model := NewModel(nil, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.Update(ModalRequestMsg{
		ID:    "picker-1",
		Kind:  ModalKindPicker,
		Title: "Pick one",
		Options: []ModalOption{
			{ID: "a", Label: "Alpha"},
			{ID: "b", Label: "Beta"},
		},
	})

	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	if len(responses) != 1 || responses[0].OptionID != "b" || responses[0].Cancelled {
		t.Fatalf("responses = %#v", responses)
	}

	model.Update(ModalRequestMsg{
		ID:      "picker-2",
		Kind:    ModalKindPicker,
		Title:   "Pick again",
		Options: []ModalOption{{ID: "a", Label: "Alpha"}},
	})
	model.Update(key(bubbletea.KeyEsc))
	if len(responses) != 2 || !responses[1].Cancelled || responses[1].ID != "picker-2" {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestModelModalBlocksComposerInput(t *testing.T) {
	model := NewModel(nil, Options{})
	model.Update(ModalRequestMsg{
		ID:      "modal-1",
		Title:   "Confirm",
		Options: []ModalOption{{ID: "ok", Label: "OK"}},
	})

	typeText(t, model, "hello")
	if got := model.ComposerValue(); got != "" {
		t.Fatalf("composer value = %q, want empty while modal active", got)
	}
	if !strings.Contains(model.View(), "Confirm") {
		t.Fatalf("modal should still be active:\n%s", model.View())
	}
}

func TestModelSlashCommands(t *testing.T) {
	state := codextui.NewState(nil)
	state.AddMessage(codextui.RoleUser, "old")
	model := NewModel(state, Options{})

	typeText(t, model, "/model gpt-5")
	model.Update(key(bubbletea.KeyEnter))
	if state.Model != "gpt-5" {
		t.Fatalf("Model = %q, want gpt-5", state.Model)
	}
	if len(model.SubmittedPrompts()) != 0 {
		t.Fatalf("slash command should not submit a prompt")
	}
	if !strings.Contains(model.View(), "Model: gpt-5") {
		t.Fatalf("View() missing model notice:\n%s", model.View())
	}

	typeText(t, model, "/approval always")
	model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(model.View(), "Approval must be one of") {
		t.Fatalf("View() missing approval validation:\n%s", model.View())
	}

	typeText(t, model, "/clear")
	model.Update(key(bubbletea.KeyEnter))
	if len(state.Messages) != 0 {
		t.Fatalf("Messages len = %d, want 0", len(state.Messages))
	}
	if !strings.Contains(model.View(), "No messages yet.") {
		t.Fatalf("View() missing empty transcript:\n%s", model.View())
	}
}

func TestModelResumeCommandOpensSessionPickerAndSetsThread(t *testing.T) {
	now := fixedTeaTime()
	state := codextui.NewState(nil)
	var responses []ModalResponse
	model := NewModel(state, Options{
		SessionPickerCWD: `D:\repo\a`,
		SessionPickerItems: []codextui.SessionSummary{
			{
				ThreadID:  "thread-resume",
				Title:     "Resume Me",
				CWD:       `D:\repo\a`,
				Branch:    "main",
				Provider:  "openai",
				UpdatedAt: now,
			},
			{
				ThreadID:  "thread-other",
				Title:     "Other Workspace",
				CWD:       `D:\repo\b`,
				UpdatedAt: now,
			},
		},
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})

	typeText(t, model, "/resume")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"Resume a previous session", "Resume Me", "branch: main", "thread-resume"} {
		if !strings.Contains(view, want) {
			t.Fatalf("resume picker missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Other Workspace") {
		t.Fatalf("resume picker should respect cwd filter:\n%s", view)
	}

	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'1'}}))
	if state.ThreadID != "thread-resume" {
		t.Fatalf("ThreadID = %q, want thread-resume", state.ThreadID)
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != string(codextui.SessionSelectionResume) || responses[0].Picker.Value != "thread-resume" {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestModelForkCommandUsesSessionAction(t *testing.T) {
	state := codextui.NewState(nil)
	now := fixedTeaTime()
	var responses []ModalResponse
	var actions []codextui.SessionSelection
	model := NewModel(state, Options{
		SessionPickerItems: []codextui.SessionSummary{{
			ThreadID:  "thread-source",
			Title:     "Source",
			CWD:       `D:\repo\a`,
			UpdatedAt: now,
		}},
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			actions = append(actions, selection)
			return &codextui.SessionSummary{
				ThreadID:  "thread-forked",
				Title:     "Forked",
				CWD:       `D:\repo\a`,
				UpdatedAt: now.Add(time.Minute),
			}, nil
		},
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})

	typeText(t, model, "/fork")
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Fork a previous session") || !strings.Contains(view, "Source") {
		t.Fatalf("fork picker view:\n%s", view)
	}
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'1'}}))
	if len(actions) != 1 || actions[0].Kind != codextui.SessionSelectionFork || actions[0].Target.ThreadID != "thread-source" {
		t.Fatalf("actions = %#v", actions)
	}
	if state.ThreadID != "thread-forked" {
		t.Fatalf("ThreadID = %q, want thread-forked", state.ThreadID)
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != string(codextui.SessionSelectionFork) || responses[0].Picker.Value != "thread-forked" {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestModelDeleteCommandConfirmsAndRemovesSession(t *testing.T) {
	now := fixedTeaTime()
	var responses []ModalResponse
	var actions []codextui.SessionSelection
	model := NewModel(nil, Options{
		SessionPickerItems: []codextui.SessionSummary{{
			ThreadID:  "thread-delete",
			Title:     "Delete Me",
			UpdatedAt: now,
		}},
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			actions = append(actions, selection)
			return nil, nil
		},
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})

	typeText(t, model, "/delete")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'1'}}))
	if len(responses) != 0 {
		t.Fatalf("responses before confirmation = %#v", responses)
	}
	if view := model.View(); !strings.Contains(view, "Confirm session delete") || !strings.Contains(view, "This cannot be undone.") {
		t.Fatalf("delete confirmation view:\n%s", view)
	}
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'1'}}))
	if len(actions) != 1 || actions[0].Kind != codextui.SessionSelectionDelete || actions[0].Target.ThreadID != "thread-delete" {
		t.Fatalf("actions = %#v", actions)
	}
	if len(model.sessionItems) != 0 {
		t.Fatalf("sessionItems = %#v, want empty after delete", model.sessionItems)
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != string(codextui.SessionSelectionDelete) || responses[0].Picker.Value != "thread-delete" {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestModelAttachmentCommandsRenderAndSubmit(t *testing.T) {
	var requests []SubmitRequest
	model := NewModel(nil, Options{
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})

	typeText(t, model, `/attach D:\repo\notes.md`)
	model.Update(key(bubbletea.KeyEnter))
	typeText(t, model, `/image D:\repo\diagram.png`)
	model.Update(key(bubbletea.KeyEnter))
	typeText(t, model, `/url-image https://example.test/preview.png`)
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"Attachments:", "file: notes.md", "image: diagram.png", "remote image: https://example.test/preview.png"} {
		if !strings.Contains(view, want) {
			t.Fatalf("attachment view missing %q:\n%s", want, view)
		}
	}
	if got := model.ComposerAttachments(); len(got) != 3 {
		t.Fatalf("attachments = %#v", got)
	}

	typeText(t, model, "review these")
	model.Update(key(bubbletea.KeyEnter))
	if len(requests) != 1 || requests[0].Prompt != "review these" || len(requests[0].Attachments) != 3 {
		t.Fatalf("submit requests = %#v", requests)
	}
	if requests[0].Attachments[0].Kind != bottompane.AttachmentFile || !strings.HasSuffix(requests[0].Attachments[0].Path, `notes.md`) {
		t.Fatalf("file attachment = %#v", requests[0].Attachments[0])
	}
	if requests[0].Attachments[1].Kind != bottompane.AttachmentImage || !strings.HasSuffix(requests[0].Attachments[1].Path, `diagram.png`) {
		t.Fatalf("image attachment = %#v", requests[0].Attachments[1])
	}
	if requests[0].Attachments[2].Kind != bottompane.AttachmentRemoteImage || requests[0].Attachments[2].URL != "https://example.test/preview.png" {
		t.Fatalf("remote image attachment = %#v", requests[0].Attachments[2])
	}
	submitted := model.SubmittedPrompts()
	if len(submitted) != 1 {
		t.Fatalf("SubmittedPrompts = %#v", submitted)
	}
	for _, want := range []string{"review these", "Attachments:", "- file: ", "notes.md", "- image: ", "diagram.png", "- image_url: https://example.test/preview.png"} {
		if !strings.Contains(submitted[0], want) {
			t.Fatalf("submitted prompt missing %q:\n%s", want, submitted[0])
		}
	}
	if got := model.SubmittedRequests(); len(got) != 1 || len(got[0].Attachments) != 3 {
		t.Fatalf("SubmittedRequests = %#v", got)
	}
	if got := model.ComposerAttachments(); len(got) != 0 {
		t.Fatalf("attachments after submit = %#v", got)
	}
}

func TestModelClearAttachmentsCommand(t *testing.T) {
	model := NewModel(nil, Options{})
	typeText(t, model, `/attach D:\repo\notes.md`)
	model.Update(key(bubbletea.KeyEnter))
	typeText(t, model, "/clear-attachments")
	model.Update(key(bubbletea.KeyEnter))
	if got := model.ComposerAttachments(); len(got) != 0 {
		t.Fatalf("attachments after clear = %#v", got)
	}
	if !strings.Contains(model.View(), "Cleared 1 attachment.") {
		t.Fatalf("clear notice missing:\n%s", model.View())
	}
}

func TestModelCommandWithoutArgsOpensModelPicker(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-b"})
	var responses []ModalResponse
	model := NewModel(state, Options{
		ModelPickerOptions: []codextui.ModelPickerOption{
			{ID: "gpt-a", Label: "GPT A", Description: "first", IsDefault: true},
			{ID: "gpt-b", Label: "GPT B", Description: "current"},
		},
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})

	typeText(t, model, "/model")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"Select Model", "GPT A", "GPT B", "current"} {
		if !strings.Contains(view, want) {
			t.Fatalf("model picker missing %q:\n%s", want, view)
		}
	}

	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'1'}}))
	if state.Model != "gpt-a" {
		t.Fatalf("Model = %q, want gpt-a", state.Model)
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != "model" || responses[0].Picker.Value != "gpt-a" {
		t.Fatalf("responses = %#v", responses)
	}
	if !strings.Contains(model.View(), "Model: gpt-a") {
		t.Fatalf("View() missing model notice:\n%s", model.View())
	}
}

func TestModelPickerOpensReasoningPickerForMultiEffortModel(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-b", ReasoningEffort: "medium"})
	var responses []ModalResponse
	model := NewModel(state, Options{
		ModelPickerOptions: []codextui.ModelPickerOption{
			{
				ID:                     "gpt-a",
				Label:                  "GPT A",
				DefaultReasoningEffort: "medium",
				SupportedReasoningEfforts: []codextui.ReasoningEffortOption{
					{Effort: "low", Label: "Low"},
					{Effort: "medium", Label: "Medium", IsDefault: true},
					{Effort: "high", Label: "High"},
				},
			},
			{ID: "gpt-b", Label: "GPT B"},
		},
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})

	typeText(t, model, "/model")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'1'}}))
	view := model.View()
	for _, want := range []string{"Select Reasoning Level for gpt-a", "Low", "Medium", "High"} {
		if !strings.Contains(view, want) {
			t.Fatalf("reasoning picker missing %q:\n%s", want, view)
		}
	}
	if len(responses) != 0 {
		t.Fatalf("responses before reasoning selection = %#v", responses)
	}

	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'3'}}))
	if state.Model != "gpt-a" || state.ReasoningEffort != "high" {
		t.Fatalf("state model=%q reasoning=%q", state.Model, state.ReasoningEffort)
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != "model_reasoning" || responses[0].Picker.Value != "gpt-a" || responses[0].Picker.ReasoningEffort != "high" {
		t.Fatalf("responses = %#v", responses)
	}
	if !strings.Contains(model.View(), "Reasoning: high") {
		t.Fatalf("View() missing reasoning notice/status:\n%s", model.View())
	}
}

func TestModelReasoningPickerPlanOnlyScope(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:           "gpt-a",
		ReasoningEffort: "medium",
		PlanMode:        true,
	})
	var responses []ModalResponse
	model := NewModel(state, Options{
		ModelPickerOptions: planScopeModelOptions(),
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})

	typeText(t, model, "/model")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'1'}}))
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'3'}}))
	view := model.View()
	for _, want := range []string{"Apply reasoning change", "Apply to Plan mode override", "Apply to global default"} {
		if !strings.Contains(view, want) {
			t.Fatalf("plan scope picker missing %q:\n%s", want, view)
		}
	}
	if len(responses) != 0 {
		t.Fatalf("responses before scope selection = %#v", responses)
	}

	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'1'}}))
	if state.Model != "gpt-a" || state.ReasoningEffort != "medium" || state.PlanModeReasoningEffort != "high" {
		t.Fatalf("state model=%q reasoning=%q plan reasoning=%q", state.Model, state.ReasoningEffort, state.PlanModeReasoningEffort)
	}
	if state.EffectiveReasoningEffort() != "high" {
		t.Fatalf("effective reasoning = %q, want high", state.EffectiveReasoningEffort())
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != "plan_reasoning_scope" || responses[0].Picker.Scope != string(codextui.PlanReasoningScopePlanOnly) {
		t.Fatalf("responses = %#v", responses)
	}
	if !strings.Contains(model.View(), "Plan Reasoning: high") {
		t.Fatalf("View() missing Plan reasoning:\n%s", model.View())
	}
}

func TestModelReasoningPickerAllModesScope(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:                   "gpt-a",
		ReasoningEffort:         "medium",
		PlanMode:                true,
		PlanModeReasoningEffort: "high",
	})
	var responses []ModalResponse
	model := NewModel(state, Options{
		ModelPickerOptions: planScopeModelOptions(),
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})

	typeText(t, model, "/model")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'1'}}))
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'3'}}))
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'2'}}))

	if state.Model != "gpt-a" || state.ReasoningEffort != "high" || state.PlanModeReasoningEffort != "high" {
		t.Fatalf("state model=%q reasoning=%q plan reasoning=%q", state.Model, state.ReasoningEffort, state.PlanModeReasoningEffort)
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != "plan_reasoning_scope" || responses[0].Picker.Scope != string(codextui.PlanReasoningScopeAllModes) {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestModelRequestUserInputModalAnswersQuestions(t *testing.T) {
	var responses []ModalResponse
	model := NewModel(nil, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.Update(RequestUserInputMsg{
		ID: "user-input-1",
		Questions: []codextui.RequestUserInputQuestion{
			{
				Header:   "Scope",
				ID:       "scope",
				Question: "Where should this apply?",
				Options:  []codextui.RequestUserInputChoice{{Label: "Plan"}, {Label: "All"}},
			},
			{
				ID:       "notes",
				Question: "Any notes?",
			},
		},
	})
	view := model.View()
	for _, want := range []string{"Request user input", "Where should this apply?", "Plan", "All"} {
		if !strings.Contains(view, want) {
			t.Fatalf("request_user_input modal missing %q:\n%s", want, view)
		}
	}
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'2'}}))
	if len(responses) != 0 {
		t.Fatalf("responses after first question = %#v, want none", responses)
	}
	typeText(t, model, "ship it")
	model.Update(key(bubbletea.KeyEnter))
	if len(responses) != 1 {
		t.Fatalf("responses len = %d, want 1", len(responses))
	}
	response := responses[0]
	if response.ID != "user-input-1" || response.Kind != ModalKindUserInput || response.UserInput == nil {
		t.Fatalf("response = %#v", response)
	}
	if response.UserInput.Answers["scope"] != "All" || response.UserInput.Answers["notes"] != "ship it" {
		t.Fatalf("answers = %#v", response.UserInput.Answers)
	}
	if got := response.UserInput.AnswerLists["scope"]; len(got) != 1 || got[0] != "All" {
		t.Fatalf("scope answer list = %#v", got)
	}
	if got := response.UserInput.AnswerLists["notes"]; len(got) != 1 || got[0] != "ship it" {
		t.Fatalf("notes answer list = %#v", got)
	}
}

func TestModelRequestUserInputModalOptionNotes(t *testing.T) {
	var responses []ModalResponse
	model := NewModel(nil, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.Update(RequestUserInputMsg{
		ID: "user-input-notes",
		Questions: []codextui.RequestUserInputQuestion{{
			ID:       "scope",
			Question: "Where should this apply?",
			Options:  []codextui.RequestUserInputChoice{{Label: "Plan"}, {Label: "All"}},
		}},
	})
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyTab))
	typeText(t, model, "include tests")
	view := model.View()
	for _, want := range []string{"Notes: include tests", "Tab or Esc clear notes"} {
		if !strings.Contains(view, want) {
			t.Fatalf("notes modal missing %q:\n%s", want, view)
		}
	}
	model.Update(key(bubbletea.KeyEnter))
	if len(responses) != 1 || responses[0].UserInput == nil {
		t.Fatalf("responses = %#v", responses)
	}
	if got := responses[0].UserInput.AnswerLists["scope"]; len(got) != 2 || got[0] != "All" || got[1] != "user_note: include tests" {
		t.Fatalf("scope answer list = %#v", got)
	}
}

func TestModelRequestUserInputModalUnansweredConfirmation(t *testing.T) {
	var responses []ModalResponse
	model := NewModel(nil, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.Update(RequestUserInputMsg{
		ID: "user-input-unanswered",
		Questions: []codextui.RequestUserInputQuestion{
			{ID: "first", Question: "First?"},
			{ID: "second", Question: "Second?"},
		},
	})
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"Submit with unanswered questions?", "2 unanswered questions", "Proceed", "Go back"} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation modal missing %q:\n%s", want, view)
		}
	}
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'2'}}))
	if len(responses) != 0 {
		t.Fatalf("responses after go back = %#v", responses)
	}
	if !strings.Contains(model.View(), "First?") {
		t.Fatalf("expected to return to first unanswered question:\n%s", model.View())
	}
	typeText(t, model, "answered")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyEnter))
	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'1'}}))
	if len(responses) != 1 || responses[0].UserInput == nil {
		t.Fatalf("responses = %#v", responses)
	}
	if responses[0].UserInput.Answers["first"] != "answered" || responses[0].UserInput.Answers["second"] != "" {
		t.Fatalf("answers = %#v", responses[0].UserInput.Answers)
	}
}

func TestModelRequestUserInputModalAutoTimeout(t *testing.T) {
	timeoutMS := 60000
	var responses []ModalResponse
	model := NewModel(nil, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.Update(RequestUserInputMsg{
		ID:               "user-input-timeout",
		AutoResolutionMS: &timeoutMS,
		Questions: []codextui.RequestUserInputQuestion{{
			ID:       "scope",
			Question: "Where should this apply?",
			Options:  []codextui.RequestUserInputChoice{{Label: "Plan"}, {Label: "All"}},
		}},
	})
	if !strings.Contains(model.View(), "auto-resolves in 1m 00s") {
		t.Fatalf("request_user_input modal missing timeout:\n%s", model.View())
	}

	model.Update(requestUserInputTimeoutMsg{ID: "other"})
	if len(responses) != 0 {
		t.Fatalf("responses after unrelated timeout = %#v", responses)
	}
	model.Update(requestUserInputTimeoutMsg{ID: "user-input-timeout"})
	if len(responses) != 1 || responses[0].UserInput == nil || !responses[0].UserInput.TimedOut {
		t.Fatalf("responses = %#v", responses)
	}
	if responses[0].UserInput.Answers == nil || len(responses[0].UserInput.Answers) != 0 {
		t.Fatalf("timeout answers = %#v", responses[0].UserInput.Answers)
	}
	if strings.Contains(model.View(), "Where should this apply?") || strings.Contains(model.View(), "Plan") {
		t.Fatalf("modal should close after timeout:\n%s", model.View())
	}
}

func TestModelExitCommandQuits(t *testing.T) {
	model := NewModel(nil, Options{})

	typeText(t, model, "/exit")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/exit returned nil command")
	}
	if _, ok := cmd().(bubbletea.QuitMsg); !ok {
		t.Fatalf("/exit command returned %T, want QuitMsg", cmd())
	}
}

func TestModelResize(t *testing.T) {
	model := NewModel(nil, Options{})
	model.Update(bubbletea.WindowSizeMsg{Width: 100, Height: 30})
	width, height := model.Size()
	if width != 100 || height != 30 {
		t.Fatalf("Size = %dx%d, want 100x30", width, height)
	}
}

func planScopeModelOptions() []codextui.ModelPickerOption {
	return []codextui.ModelPickerOption{
		{
			ID:                     "gpt-a",
			Label:                  "GPT A",
			DefaultReasoningEffort: "medium",
			SupportedReasoningEfforts: []codextui.ReasoningEffortOption{
				{Effort: "low", Label: "Low"},
				{Effort: "medium", Label: "Medium", IsDefault: true},
				{Effort: "high", Label: "High"},
			},
		},
		{ID: "gpt-b", Label: "GPT B"},
	}
}

func fixedTeaTime() time.Time {
	return time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
}

func typeText(t *testing.T, model *Model, value string) {
	t.Helper()
	for _, r := range value {
		updated, _ := model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{r}}))
		next, ok := updated.(*Model)
		if !ok {
			t.Fatalf("Update returned %T, want *Model", updated)
		}
		model = next
	}
}

func key(keyType bubbletea.KeyType) bubbletea.KeyMsg {
	return bubbletea.KeyMsg(bubbletea.Key{Type: keyType})
}

func countRole(messages []codextui.Message, role codextui.MessageRole) int {
	count := 0
	for _, message := range messages {
		if message.Role == role {
			count++
		}
	}
	return count
}
