package tea

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	appsapi "codex_go/apps"
	"codex_go/appserver"
	"codex_go/plugin"
	"codex_go/protocol"
	"codex_go/review"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
	"codex_go/utils"
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
	cleanView := utils.StripANSI(view)

	for _, want := range []string{"Thread: thread-1", "Model: gpt-test", "› hello", "• hi there", "Enter send"} {
		if !strings.Contains(cleanView, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
}

func TestModelViewSeparatesWideTerminalRegions(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:          "gpt-test",
		ApprovalPolicy: "on-request",
		Sandbox:        "workspace-write",
	})
	state.SetThreadID("thread-wide")
	state.AddMessage(codextui.RoleAssistant, "Working through the repository.")
	model := NewModel(state, Options{Width: 120, Height: 28})

	view := utils.StripANSI(model.View())
	for _, want := range []string{" ACTIVITY ─", "╭", "╰", "Ask Codex"} {
		if !strings.Contains(view, want) {
			t.Fatalf("wide view missing region chrome %q:\n%s", want, view)
		}
	}
	if got := model.composer.Width(); got >= model.width {
		t.Fatalf("composer width = %d, want room for border within terminal width %d", got, model.width)
	}
}

func TestModelViewKeepsNarrowTerminalCompact(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 82, Height: 18})
	view := utils.StripANSI(model.View())
	if strings.Contains(view, " ACTIVITY ─") || strings.Contains(view, "╭") {
		t.Fatalf("narrow view should not spend rows or columns on region chrome:\n%s", view)
	}
}

func TestModelViewCanShowRustStyleSessionHeader(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:           "gpt-5.5",
		ReasoningEffort: "xhigh",
		CWD:             `D:\repo`,
	})
	model := NewModel(state, Options{Width: 90, Height: 18, ShowSessionHeader: true, SessionHeaderVersion: "0.142.5"})
	view := model.View()
	for _, want := range []string{"OpenAI Codex", "model:", "gpt-5.5 xhigh", "directory:", `D:\repo`} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "No messages yet.") {
		t.Fatalf("session header view should replace empty transcript:\n%s", view)
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
	if !strings.Contains(utils.StripANSI(model.View()), "› hello codex") {
		t.Fatalf("View() should include submitted user message:\n%s", model.View())
	}
}

func TestModelCtrlJInsertsComposerNewlineWithoutSubmitting(t *testing.T) {
	var requests []SubmitRequest
	model := NewModel(nil, Options{
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})

	typeText(t, model, "line one")
	model.Update(key(bubbletea.KeyCtrlJ))
	typeText(t, model, "line two")

	if len(requests) != 0 {
		t.Fatalf("requests before Enter = %#v, want none", requests)
	}
	if got := model.ComposerValue(); got != "line one\nline two" {
		t.Fatalf("ComposerValue = %q, want line one newline line two", got)
	}

	model.Update(key(bubbletea.KeyEnter))
	if len(requests) != 1 || requests[0].Prompt != "line one\nline two" {
		t.Fatalf("requests after Enter = %#v", requests)
	}
	if got := model.ComposerValue(); got != "" {
		t.Fatalf("composer after submit = %q, want empty", got)
	}
}

func TestModelEscInterruptsRunningTaskWithoutQuitting(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	interrupted := 0
	model := NewModel(state, Options{
		OnInterrupt: func() bubbletea.Cmd {
			interrupted++
			return func() bubbletea.Msg {
				return TurnInterruptedMsg{}
			}
		},
	})

	_, cmd := model.Update(key(bubbletea.KeyEsc))
	if cmd == nil {
		t.Fatal("Esc while running returned nil command")
	}
	if interrupted != 1 {
		t.Fatalf("interrupt calls = %d, want 1", interrupted)
	}
	model.Update(cmd())
	if state.Status != "idle" {
		t.Fatalf("Status = %q, want idle after interrupt", state.Status)
	}
}

func TestModelWorkingIndicatorMatchesRust(t *testing.T) {
	start := time.Unix(100, 0)
	now := start.Add(12 * time.Second)
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 18})
	model.taskStartedAt = start
	model.now = func() time.Time { return now }

	view := model.View()
	if !strings.Contains(view, "\u2022 Working (12s \u2022 esc to interrupt)") {
		t.Fatalf("Working indicator missing or not Rust-like:\n%s", view)
	}
	if !strings.Contains(view, "Ask Codex") {
		t.Fatalf("composer should remain visible below Working indicator:\n%s", view)
	}
}

func TestModelWorkingIndicatorUsesRemappedInterruptHint(t *testing.T) {
	start := time.Unix(100, 0)
	state := codextui.NewState(nil)
	state.SetStatus("running")
	cfg := codextui.NewKeymapConfig()
	if err := cfg.Set("chat", "interrupt_turn", []string{"ctrl-x"}); err != nil {
		t.Fatal(err)
	}
	model := NewModel(state, Options{Width: 80, Height: 18, KeymapConfig: cfg})
	model.taskStartedAt = start
	model.now = func() time.Time { return start }

	view := model.View()
	if !strings.Contains(view, "\u2022 Working (0s \u2022 ctrl-x to interrupt)") {
		t.Fatalf("remapped Working indicator missing:\n%s", view)
	}
	if strings.Contains(view, "esc to interrupt") {
		t.Fatalf("Working indicator should not show default Esc after remap:\n%s", view)
	}
}

func TestModelCtrlGExternalEditorAppliesEditedDraft(t *testing.T) {
	var seeds []string
	model := NewModel(nil, Options{
		OnExternalEditor: func(seed string) bubbletea.Cmd {
			seeds = append(seeds, seed)
			return func() bubbletea.Msg {
				return ExternalEditorFinishedMsg{Text: seed + "\nedited\n\n"}
			}
		},
	})

	typeText(t, model, "draft")
	_, cmd := model.Update(key(bubbletea.KeyCtrlG))
	if cmd == nil {
		t.Fatal("Ctrl+G returned nil command")
	}
	if got := seeds; len(got) != 1 || got[0] != "draft" {
		t.Fatalf("external editor seeds = %#v, want draft", got)
	}
	if !strings.Contains(model.View(), "Save and close external editor to continue.") {
		t.Fatalf("View() missing external editor hint:\n%s", model.View())
	}

	model.Update(cmd())
	if got := model.ComposerValue(); got != "draft\nedited" {
		t.Fatalf("ComposerValue after editor = %q, want edited draft", got)
	}
	if strings.Contains(model.View(), "Save and close external editor") {
		t.Fatalf("external editor hint should close:\n%s", model.View())
	}
}

func TestModelSlashEditorOpensExternalEditor(t *testing.T) {
	var seeds []string
	model := NewModel(nil, Options{
		OnExternalEditor: func(seed string) bubbletea.Cmd {
			seeds = append(seeds, seed)
			return func() bubbletea.Msg {
				return ExternalEditorFinishedMsg{Text: "from slash"}
			}
		},
	})

	typeText(t, model, "/editor")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/editor returned nil command")
	}
	if got := seeds; len(got) != 1 || got[0] != "" {
		t.Fatalf("/editor seeds = %#v, want one empty seed", got)
	}

	model.Update(cmd())
	if got := model.ComposerValue(); got != "from slash" {
		t.Fatalf("ComposerValue after /editor = %q, want from slash", got)
	}
	if len(model.SubmittedPrompts()) != 0 {
		t.Fatalf("/editor should not submit prompts: %#v", model.SubmittedPrompts())
	}
}

func TestModelKeymapCommandRendersCatalog(t *testing.T) {
	model := NewModel(nil, Options{})

	typeText(t, model, "/keymap")
	model.Update(key(bubbletea.KeyEnter))
	if got := len(model.State.Messages); got != 1 {
		t.Fatalf("messages len = %d, want 1", got)
	}
	catalog := model.State.Messages[0].Text

	for _, want := range []string{
		"Codex TUI keymap:",
		"Open External Editor",
		"ctrl-g",
		"Insert Newline",
		"ctrl-j",
		"Approve For Session",
	} {
		if !strings.Contains(catalog, want) {
			t.Fatalf("/keymap catalog missing %q:\n%s", want, catalog)
		}
	}
	if len(model.SubmittedPrompts()) != 0 {
		t.Fatalf("/keymap should not submit prompts: %#v", model.SubmittedPrompts())
	}
}

func TestModelKeymapCommandAppliesRuntimeRemap(t *testing.T) {
	var seeds []string
	model := NewModel(nil, Options{
		OnExternalEditor: func(seed string) bubbletea.Cmd {
			seeds = append(seeds, seed)
			return nil
		},
	})

	typeText(t, model, "/keymap set global.open_external_editor ctrl-e")
	model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(model.State.Messages[0].Text, "ctrl-e") {
		t.Fatalf("keymap set message = %#v", model.State.Messages)
	}

	typeText(t, model, "draft")
	_, cmd := model.Update(key(bubbletea.KeyCtrlG))
	if cmd != nil || len(seeds) != 0 {
		t.Fatalf("Ctrl+G should be remapped away, cmd=%v seeds=%#v", cmd, seeds)
	}
	_, cmd = model.Update(key(bubbletea.KeyCtrlE))
	if len(seeds) != 1 || seeds[0] != "draft" {
		t.Fatalf("Ctrl+E remap failed, cmd=%v seeds=%#v", cmd, seeds)
	}
}

func TestModelKeymapSubmitAndInterruptRemap(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	interrupts := 0
	var requests []SubmitRequest
	model := NewModel(state, Options{
		KeymapConfig: func() *codextui.KeymapConfig {
			cfg := codextui.NewKeymapConfig()
			if err := cfg.Set("composer", "submit", []string{"ctrl-s"}); err != nil {
				t.Fatal(err)
			}
			if err := cfg.Set("chat", "interrupt_turn", []string{"ctrl-x"}); err != nil {
				t.Fatal(err)
			}
			return cfg
		}(),
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
		OnInterrupt: func() bubbletea.Cmd {
			interrupts++
			return func() bubbletea.Msg { return TurnInterruptedMsg{} }
		},
	})

	typeText(t, model, "queued")
	model.Update(key(bubbletea.KeyEnter))
	if got := model.QueuedRequests(); len(got) != 0 {
		t.Fatalf("Enter should not submit after remap, queued=%#v", got)
	}
	model.Update(key(bubbletea.KeyCtrlS))
	if got := model.QueuedRequests(); len(got) != 1 || got[0].Prompt != "queued" {
		t.Fatalf("Ctrl+S should queue while running, queued=%#v requests=%#v", got, requests)
	}
	if _, cmd := model.Update(key(bubbletea.KeyEsc)); cmd != nil || interrupts != 0 {
		t.Fatalf("Esc should be remapped away, interrupts=%d cmd=%v", interrupts, cmd)
	}
	_, cmd := model.Update(key(bubbletea.KeyCtrlX))
	if cmd == nil || interrupts != 1 {
		t.Fatalf("Ctrl+X should interrupt, interrupts=%d cmd=%v", interrupts, cmd)
	}
}

func TestModelExternalEditorReportsError(t *testing.T) {
	model := NewModel(nil, Options{
		OnExternalEditor: func(seed string) bubbletea.Cmd {
			return func() bubbletea.Msg {
				return ExternalEditorFinishedMsg{Err: errors.New("boom")}
			}
		},
	})

	typeText(t, model, "draft")
	_, cmd := model.Update(key(bubbletea.KeyCtrlG))
	if cmd == nil {
		t.Fatal("Ctrl+G returned nil command")
	}
	model.Update(cmd())

	if got := model.ComposerValue(); got != "draft" {
		t.Fatalf("ComposerValue after editor error = %q, want draft", got)
	}
	if !strings.Contains(model.View(), "Failed to open editor: boom") {
		t.Fatalf("View() missing editor error:\n%s", model.View())
	}
	if strings.Contains(model.View(), "Save and close external editor") {
		t.Fatalf("external editor hint should close after error:\n%s", model.View())
	}
}

func TestModelPasteBurstEnterInsertsNewlineWithoutSubmitting(t *testing.T) {
	now := time.Unix(0, 0)
	var requests []SubmitRequest
	model := NewModel(nil, Options{
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})
	model.now = func() time.Time { return now }

	model.Update(runes("hello"))
	now = now.Add(10 * time.Millisecond)
	model.Update(key(bubbletea.KeyEnter))
	if len(requests) != 0 {
		t.Fatalf("requests after paste Enter = %#v, want none", requests)
	}
	if got := model.ComposerValue(); got != "hello\n" {
		t.Fatalf("composer after paste Enter = %q, want hello newline", got)
	}

	typeText(t, model, "there")
	now = now.Add(bottompane.PasteEnterSuppressWindow + time.Millisecond)
	model.Update(key(bubbletea.KeyEnter))
	if len(requests) != 1 || requests[0].Prompt != "hello\nthere" {
		t.Fatalf("requests after final Enter = %#v", requests)
	}
}

func TestModelPasteBurstDoesNotBlockSlashCommandEnter(t *testing.T) {
	now := time.Unix(0, 0)
	model := NewModel(nil, Options{})
	model.now = func() time.Time { return now }

	model.Update(runes("/status"))
	now = now.Add(10 * time.Millisecond)
	model.Update(key(bubbletea.KeyEnter))

	if got := model.ComposerValue(); got != "" {
		t.Fatalf("composer after slash command = %q, want empty", got)
	}
	if countRole(model.State.Messages, codextui.RoleSystem) != 1 {
		t.Fatalf("system messages = %#v, want one status message", model.State.Messages)
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

func TestModelCtrlCInterruptsRunningTaskWithoutQuitting(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	interrupts := 0
	model := NewModel(state, Options{
		OnInterrupt: func() bubbletea.Cmd {
			interrupts++
			return func() bubbletea.Msg { return TurnInterruptedMsg{} }
		},
	})

	_, cmd := model.Update(key(bubbletea.KeyCtrlC))
	if cmd == nil {
		t.Fatal("running Ctrl+C returned nil command")
	}
	if _, ok := cmd().(bubbletea.QuitMsg); ok {
		t.Fatal("running Ctrl+C should interrupt, not quit")
	}
	if interrupts != 1 {
		t.Fatalf("interrupts = %d, want 1", interrupts)
	}

	model.Update(TurnInterruptedMsg{})
	if state.Status != "idle" {
		t.Fatalf("status after interrupt = %q, want idle", state.Status)
	}
	if countRole(state.Messages, codextui.RoleHistory) != 1 {
		t.Fatalf("history messages after interrupt = %#v, want one", state.Messages)
	}
	if !strings.Contains(model.View(), "Interrupted current turn") {
		t.Fatalf("interrupt notice missing:\n%s", model.View())
	}
}

func TestModelInterruptedPromptRemainsInSubmittedHistoryLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{OnSubmit: func(string) bubbletea.Cmd { return nil }, OnInterrupt: func() bubbletea.Cmd { return func() bubbletea.Msg { return TurnInterruptedMsg{} } }})
	typeText(t, model, "keep this interrupted prompt")
	model.Update(key(bubbletea.KeyEnter))
	state.SetStatus("running")
	_, command := model.Update(key(bubbletea.KeyCtrlC))
	if command == nil {
		t.Fatal("interrupt command = nil")
	}
	model.Update(command())
	if got := model.SubmittedPrompts(); len(got) != 1 || got[0] != "keep this interrupted prompt" {
		t.Fatalf("submitted prompts = %#v", got)
	}
}

func TestModelCtrlCQuitsWhenIdle(t *testing.T) {
	model := NewModel(nil, Options{})
	_, cmd := model.Update(key(bubbletea.KeyCtrlC))
	if cmd == nil {
		t.Fatal("idle Ctrl+C returned nil command")
	}
	if _, ok := cmd().(bubbletea.QuitMsg); !ok {
		t.Fatalf("idle Ctrl+C returned %T, want QuitMsg", cmd())
	}
}

func TestModelTracksTerminalFocusMessages(t *testing.T) {
	model := NewModel(nil, Options{})
	if !model.TerminalFocused() {
		t.Fatal("new model should start focused")
	}
	typeText(t, model, "draft")
	model.Update(bubbletea.BlurMsg{})
	if model.TerminalFocused() {
		t.Fatal("BlurMsg did not clear terminal focus")
	}
	if got := model.ComposerValue(); got != "draft" {
		t.Fatalf("composer after blur = %q, want draft", got)
	}
	model.Update(bubbletea.FocusMsg{})
	if !model.TerminalFocused() {
		t.Fatal("FocusMsg did not restore terminal focus")
	}
	if got := model.ComposerValue(); got != "draft" {
		t.Fatalf("composer after focus = %q, want draft", got)
	}
}

func TestModelPostsTurnCompleteNotificationWhenUnfocused(t *testing.T) {
	var posts []string
	model := NewModel(nil, Options{
		OnPostNotification: func(message string, method codextui.NotificationMethod) bubbletea.Cmd {
			return func() bubbletea.Msg {
				posts = append(posts, message)
				return nil
			}
		},
		NotificationCondition: codextui.NotificationConditionUnfocused,
	})
	model.Update(bubbletea.BlurMsg{})

	_, cmd := model.Update(TurnCompletedMsg{AssistantMessage: "done well"})
	runTeaCmd(t, model, cmd)

	if len(posts) != 1 || posts[0] != "done well" {
		t.Fatalf("notification posts = %#v, want turn complete preview", posts)
	}
}

func TestModelPostsTurnCompleteNotificationFromTranscriptWhenMessageEmpty(t *testing.T) {
	var posts []string
	model := NewModel(nil, Options{
		OnPostNotification: func(message string, method codextui.NotificationMethod) bubbletea.Cmd {
			return func() bubbletea.Msg {
				posts = append(posts, message)
				return nil
			}
		},
	})
	model.Update(bubbletea.BlurMsg{})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.AgentMessageItem("msg-1", "remote done"))})

	_, cmd := model.Update(TurnCompletedMsg{ThreadID: "thread-1"})
	runTeaCmd(t, model, cmd)

	if len(posts) != 1 || posts[0] != "remote done" {
		t.Fatalf("notification posts = %#v, want transcript preview", posts)
	}
}

func TestModelSuppressesNotificationWhenFocused(t *testing.T) {
	var posts []string
	model := NewModel(nil, Options{
		OnPostNotification: func(message string, method codextui.NotificationMethod) bubbletea.Cmd {
			return func() bubbletea.Msg {
				posts = append(posts, message)
				return nil
			}
		},
		NotificationCondition: codextui.NotificationConditionUnfocused,
	})

	_, cmd := model.Update(TurnCompletedMsg{AssistantMessage: "done well"})
	runTeaCmd(t, model, cmd)

	if len(posts) != 0 {
		t.Fatalf("notification posts while focused = %#v, want none", posts)
	}
}

func TestModelPostsApprovalNotificationWhenUnfocused(t *testing.T) {
	var posts []string
	model := NewModel(nil, Options{
		OnPostNotification: func(message string, method codextui.NotificationMethod) bubbletea.Cmd {
			return func() bubbletea.Msg {
				posts = append(posts, message)
				return nil
			}
		},
	})
	model.Update(bubbletea.BlurMsg{})

	_, cmd := model.Update(ApprovalRequestMsg{
		ID:      "approval-1",
		Command: "go test ./...",
	})
	runTeaCmd(t, model, cmd)

	if len(posts) != 1 || posts[0] != "Approval requested: go test ./..." {
		t.Fatalf("approval notification posts = %#v", posts)
	}
}

func TestModelPostsElicitationNotificationWhenUnfocused(t *testing.T) {
	var posts []string
	model := NewModel(nil, Options{
		OnPostNotification: func(message string, method codextui.NotificationMethod) bubbletea.Cmd {
			return func() bubbletea.Msg {
				posts = append(posts, message)
				return nil
			}
		},
	})
	model.Update(bubbletea.BlurMsg{})

	_, cmd := model.Update(ElicitationRequestMsg{
		ID:         "elicitation-1",
		ServerName: "docs",
		Message:    "Open docs URL?",
		URL:        "https://example.test/docs",
	})
	runTeaCmd(t, model, cmd)

	if len(posts) != 1 || posts[0] != "Approval requested by docs" {
		t.Fatalf("elicitation notification posts = %#v", posts)
	}
}

func TestModelNotificationUsesConfiguredMethod(t *testing.T) {
	var methods []codextui.NotificationMethod
	model := NewModel(nil, Options{
		NotificationMethod: codextui.NotificationMethodBEL,
		OnPostNotification: func(message string, method codextui.NotificationMethod) bubbletea.Cmd {
			return func() bubbletea.Msg {
				methods = append(methods, method)
				return nil
			}
		},
	})
	model.Update(bubbletea.BlurMsg{})

	_, cmd := model.Update(TurnCompletedMsg{AssistantMessage: "done"})
	runTeaCmd(t, model, cmd)

	if len(methods) != 1 || methods[0] != codextui.NotificationMethodBEL {
		t.Fatalf("notification methods = %#v, want bel", methods)
	}
}

func TestModelTranscriptNavigationPreservesScrollPosition(t *testing.T) {
	state := codextui.NewState(nil)
	for i := 0; i < 30; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("event %02d\nmore detail", i))
	}
	model := NewModel(state, Options{Width: 60, Height: 10})
	bottomOffset := model.transcript.YOffset
	if bottomOffset <= 0 {
		t.Fatalf("initial transcript offset = %d, want scrollable bottom", bottomOffset)
	}

	model.Update(key(bubbletea.KeyPgUp))
	scrolledOffset := model.transcript.YOffset
	if scrolledOffset >= bottomOffset {
		t.Fatalf("PageUp offset = %d, want less than bottom %d", scrolledOffset, bottomOffset)
	}

	_ = model.View()
	if got := model.transcript.YOffset; got != scrolledOffset {
		t.Fatalf("View() reset scroll offset to %d, want %d", got, scrolledOffset)
	}

	state.AddMessage(codextui.RoleAssistant, "new output while reading")
	_ = model.View()
	if got := model.transcript.YOffset; got != scrolledOffset {
		t.Fatalf("new content reset scroll offset to %d, want %d", got, scrolledOffset)
	}

	model.Update(key(bubbletea.KeyEnd))
	if !model.transcript.AtBottom() {
		t.Fatalf("End did not move transcript to bottom; offset=%d", model.transcript.YOffset)
	}
	model.Update(key(bubbletea.KeyHome))
	if !model.transcript.AtTop() {
		t.Fatalf("Home did not move transcript to top; offset=%d", model.transcript.YOffset)
	}
	model.Update(key(bubbletea.KeyEnd))
	state.AddMessage(codextui.RoleAssistant, "tail follows")
	_ = model.View()
	if !model.transcript.AtBottom() {
		t.Fatalf("bottom transcript did not follow new content; offset=%d", model.transcript.YOffset)
	}
}

func TestModelActivityFollowTracksRunningEventsUntilUserScrolls(t *testing.T) {
	state := codextui.NewState(nil)
	for i := 0; i < 30; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("initial event %02d", i))
	}
	model := NewModel(state, Options{Width: 72, Height: 10})
	if !model.activityFollow || !model.transcript.AtBottom() {
		t.Fatalf("initial activity follow=%v atBottom=%v", model.activityFollow, model.transcript.AtBottom())
	}

	state.AddMessage(codextui.RoleSystem, "running event follows")
	model.refreshTranscript()
	if !model.transcript.AtBottom() {
		t.Fatalf("running event did not follow to bottom; offset=%d", model.transcript.YOffset)
	}

	model.Update(key(bubbletea.KeyPgUp))
	if model.activityFollow {
		t.Fatal("PageUp should pause activity follow")
	}
	offset := model.transcript.YOffset
	state.AddMessage(codextui.RoleSystem, "running event while reading history")
	model.refreshTranscript()
	if model.transcript.YOffset != offset {
		t.Fatalf("history reading offset changed from %d to %d", offset, model.transcript.YOffset)
	}

	model.Update(key(bubbletea.KeyEnd))
	if !model.activityFollow || !model.transcript.AtBottom() {
		t.Fatalf("End should resume activity follow; follow=%v offset=%d", model.activityFollow, model.transcript.YOffset)
	}
}

func TestModelTranscriptOverlayOpensScrollsAndCloses(t *testing.T) {
	state := codextui.NewState(nil)
	for i := 0; i < 40; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("event %02d\nmore detail", i))
	}
	model := NewModel(state, Options{Width: 60, Height: 10})

	updated, openCmd := model.Update(key(bubbletea.KeyCtrlT))
	model = updated.(*Model)
	if model.overlay == nil {
		t.Fatal("Ctrl+T did not open transcript overlay")
	}
	view := model.View()
	if !strings.Contains(view, "T R A N S C R I P T") {
		t.Fatalf("overlay view missing title:\n%s", view)
	}
	if strings.Contains(view, "Ask Codex") {
		t.Fatalf("overlay should hide composer:\n%s", view)
	}
	if !model.overlay.AtBottom() || model.overlay.YOffset() <= 0 {
		t.Fatalf("overlay initial offset=%d atBottom=%v, want scrollable bottom", model.overlay.YOffset(), model.overlay.AtBottom())
	}
	if !batchContainsMessageType(openCmd, bubbletea.EnableMouseCellMotion()) {
		t.Fatal("opening transcript overlay did not enable mouse tracking")
	}

	beforeWheel := model.overlay.YOffset()
	model.Update(bubbletea.MouseMsg{Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonWheelUp})
	if got := model.overlay.YOffset(); got >= beforeWheel {
		t.Fatalf("mouse wheel offset = %d, want less than %d", got, beforeWheel)
	}

	model.Update(key(bubbletea.KeyHome))
	if !model.overlay.AtTop() {
		t.Fatalf("Home did not move overlay to top; offset=%d", model.overlay.YOffset())
	}
	model.Update(key(bubbletea.KeyEnd))
	if !model.overlay.AtBottom() {
		t.Fatalf("End did not move overlay to bottom; offset=%d", model.overlay.YOffset())
	}

	updated, cmd := model.Update(key(bubbletea.KeyCtrlC))
	model = updated.(*Model)
	if model.overlay != nil {
		t.Fatal("Ctrl+C did not close transcript overlay")
	}
	if cmd != nil {
		if _, ok := cmd().(bubbletea.QuitMsg); ok {
			t.Fatal("Ctrl+C inside transcript overlay should not quit")
		}
	}
	if !batchContainsMessageType(cmd, bubbletea.DisableMouse()) {
		t.Fatal("closing transcript overlay did not disable mouse tracking")
	}

	updated, _ = model.Update(key(bubbletea.KeyCtrlT))
	model = updated.(*Model)
	updated, _ = model.Update(runes("q"))
	model = updated.(*Model)
	if model.overlay != nil {
		t.Fatal("q did not close transcript overlay")
	}
}

func batchContainsMessageType(cmd bubbletea.Cmd, want bubbletea.Msg) bool {
	if cmd == nil {
		return false
	}
	message := cmd()
	if fmt.Sprintf("%T", message) == fmt.Sprintf("%T", want) {
		return true
	}
	batch, ok := message.(bubbletea.BatchMsg)
	if !ok {
		return false
	}
	for _, child := range batch {
		if child != nil && fmt.Sprintf("%T", child()) == fmt.Sprintf("%T", want) {
			return true
		}
	}
	return false
}

func TestModelTranscriptOverlayPreservesScrollAndFollowsTail(t *testing.T) {
	state := codextui.NewState(nil)
	for i := 0; i < 35; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("event %02d\nmore detail", i))
	}
	model := NewModel(state, Options{Width: 60, Height: 10})
	updated, _ := model.Update(key(bubbletea.KeyCtrlT))
	model = updated.(*Model)

	model.Update(key(bubbletea.KeyHome))
	_ = model.View()
	offset := model.overlay.YOffset()
	state.AddMessage(codextui.RoleAssistant, "new tail while reading")
	_ = model.View()
	if got := model.overlay.YOffset(); got != offset {
		t.Fatalf("overlay new content offset=%d, want preserved %d", got, offset)
	}

	model.Update(key(bubbletea.KeyEnd))
	state.AddMessage(codextui.RoleAssistant, "tail follows in overlay")
	_ = model.View()
	if !model.overlay.AtBottom() {
		t.Fatalf("bottom overlay did not follow new content; offset=%d", model.overlay.YOffset())
	}
	if !strings.Contains(model.overlay.Content(), "tail follows in overlay") {
		t.Fatalf("overlay content missing appended tail")
	}
}

func TestModelCopiesLastAgentResponse(t *testing.T) {
	state := codextui.NewState(nil)
	state.AddMessage(codextui.RoleAssistant, "first")
	state.AddMessage(codextui.RoleSystem, "notice")
	state.AddMessage(codextui.RoleAssistant, "second")
	var copied string
	model := NewModel(state, Options{
		OnClipboardWrite: func(text string) error {
			copied = text
			return nil
		},
	})

	model.Update(key(bubbletea.KeyCtrlO))
	if copied != "second" {
		t.Fatalf("copied = %q, want second", copied)
	}
	if !strings.Contains(model.View(), "Copied last agent response.") {
		t.Fatalf("copy notice missing:\n%s", model.View())
	}
}

func TestModelCopyLastAgentResponseHandlesEmpty(t *testing.T) {
	called := false
	model := NewModel(codextui.NewState(nil), Options{
		OnClipboardWrite: func(text string) error {
			called = true
			return nil
		},
	})

	model.Update(key(bubbletea.KeyCtrlO))
	if called {
		t.Fatal("clipboard writer should not be called without an agent response")
	}
	if !strings.Contains(model.View(), "No agent response to copy.") {
		t.Fatalf("empty copy notice missing:\n%s", model.View())
	}
}

func TestModelSlashCopyLastAgentResponse(t *testing.T) {
	state := codextui.NewState(nil)
	state.AddMessage(codextui.RoleAssistant, "final answer")
	var copied string
	model := NewModel(state, Options{
		OnClipboardWrite: func(text string) error {
			copied = text
			return nil
		},
	})

	typeText(t, model, "/copy")
	model.Update(key(bubbletea.KeyEnter))
	if copied != "final answer" {
		t.Fatalf("copied = %q, want final answer", copied)
	}
	if len(model.SubmittedPrompts()) != 0 {
		t.Fatalf("slash copy should not submit a prompt")
	}
	if !strings.Contains(model.View(), "Copied last agent response.") {
		t.Fatalf("copy notice missing:\n%s", model.View())
	}
}

func TestModelRawCommandTogglesScrollback(t *testing.T) {
	state := codextui.NewState(nil)
	state.AddMessage(codextui.RoleAssistant, "final answer")
	state.AddHistoryLines([]string{"• Tool call", "  | display"}, []string{"Tool call", "display"})
	model := NewModel(state, Options{
		Width:           80,
		Height:          12,
		StatusLineItems: []string{"raw-output"},
	})

	if rich := model.View(); !strings.Contains(rich, "• final answer") || !strings.Contains(rich, "Tool call") {
		t.Fatalf("rich transcript missing expected rendering:\n%s", rich)
	}

	typeText(t, model, "/raw on")
	model.Update(key(bubbletea.KeyEnter))
	if !model.rawOutput {
		t.Fatal("/raw on did not enable raw output")
	}
	rawTranscript := model.transcript.View()
	if strings.Contains(rawTranscript, "• final answer") || strings.Contains(rawTranscript, "• Tool call") {
		t.Fatalf("raw transcript still contains rich formatting:\n%s", rawTranscript)
	}
	for _, want := range []string{"final answer", "Tool call", "display"} {
		if !strings.Contains(rawTranscript, want) {
			t.Fatalf("raw transcript missing %q:\n%s", want, rawTranscript)
		}
	}
	if view := model.View(); !strings.Contains(view, rawOutputModeOnNotice) || !strings.Contains(view, "raw output") {
		t.Fatalf("raw mode view missing notice/status item:\n%s", view)
	}

	typeText(t, model, "/raw off")
	model.Update(key(bubbletea.KeyEnter))
	if model.rawOutput {
		t.Fatal("/raw off did not disable raw output")
	}
	if rich := model.transcript.View(); !strings.Contains(rich, "• final answer") || !strings.Contains(rich, "Tool call") {
		t.Fatalf("rich transcript was not restored:\n%s", rich)
	}

	typeText(t, model, "/raw maybe")
	model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(model.View(), rawOutputUsage) {
		t.Fatalf("invalid raw command missing usage:\n%s", model.View())
	}
}

func TestModelAltRTogglesRawScrollback(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})

	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'r'}, Alt: true}))
	if !model.rawOutput {
		t.Fatal("Alt+R did not enable raw output")
	}
	if !strings.Contains(model.View(), rawOutputModeOnNotice) {
		t.Fatalf("Alt+R notice missing:\n%s", model.View())
	}

	model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune{'r'}, Alt: true}))
	if model.rawOutput {
		t.Fatal("second Alt+R did not disable raw output")
	}
}

func TestModelDiffCommandOpensPager(t *testing.T) {
	var cwd string
	model := NewModel(codextui.NewState(nil), Options{
		Width:            60,
		Height:           10,
		SessionPickerCWD: `D:\repo`,
		OnReadGitDiff: func(value string) (string, bool, error) {
			cwd = value
			return "diff --git a/app.go b/app.go\n@@\n-old\n+new\n", true, nil
		},
	})

	typeText(t, model, "/diff")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/diff did not return a diff command")
	}
	if !strings.Contains(model.View(), "Computing diff...") {
		t.Fatalf("diff loading notice missing:\n%s", model.View())
	}

	updated, _ := model.Update(cmd())
	model = updated.(*Model)
	if cwd != `D:\repo` {
		t.Fatalf("diff cwd = %q, want repo cwd", cwd)
	}
	if model.overlay == nil {
		t.Fatal("diff result did not open pager overlay")
	}
	view := model.View()
	for _, want := range []string{"D I F F", "diff --git a/app.go b/app.go", "+new"} {
		if !strings.Contains(view, want) {
			t.Fatalf("diff pager missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Ask Codex") {
		t.Fatalf("diff pager should hide composer:\n%s", view)
	}
}

func TestModelDiffCommandHandlesNoRepoAndEmptyDiff(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		OnReadGitDiff: func(string) (string, bool, error) {
			return "", false, nil
		},
	})
	typeText(t, model, "/diff")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/diff no-repo returned nil command")
	}
	updated, _ := model.Update(cmd())
	model = updated.(*Model)
	if !strings.Contains(model.View(), "`/diff` - not inside a git repository") {
		t.Fatalf("no-repo diff message missing:\n%s", model.View())
	}

	model = NewModel(codextui.NewState(nil), Options{
		OnReadGitDiff: func(string) (string, bool, error) {
			return "", true, nil
		},
	})
	typeText(t, model, "/diff")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	updated, _ = model.Update(cmd())
	model = updated.(*Model)
	if !strings.Contains(model.View(), "No changes detected.") {
		t.Fatalf("empty diff message missing:\n%s", model.View())
	}
}

func TestModelPsAndStopCommands(t *testing.T) {
	stopCalls := 0
	model := NewModel(codextui.NewState(nil), Options{
		Width:  80,
		Height: 18,
		BackgroundProcesses: []historycell.UnifiedExecProcessDetails{{
			CommandDisplay: "go test ./...",
			RecentChunks:   []string{"ok package"},
		}},
		OnStopBackgroundTerminals: func() bubbletea.Cmd {
			stopCalls++
			return nil
		},
	})

	typeText(t, model, "/ps")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"/ps", "Background terminals", "go test ./...", "ok package"} {
		if !strings.Contains(view, want) {
			t.Fatalf("/ps view missing %q:\n%s", want, view)
		}
	}
	if len(model.SubmittedPrompts()) != 0 {
		t.Fatalf("/ps should not submit prompts: %#v", model.SubmittedPrompts())
	}

	typeText(t, model, "/stop")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd != nil {
		_ = cmd()
	}
	if stopCalls != 1 {
		t.Fatalf("stop calls = %d, want 1", stopCalls)
	}
	if len(model.backgroundProcesses) != 0 {
		t.Fatalf("background processes after stop = %#v", model.backgroundProcesses)
	}
	if !strings.Contains(model.View(), "Stopping all background terminals.") {
		t.Fatalf("/stop notice missing:\n%s", model.View())
	}

	typeText(t, model, "/ps")
	model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(model.View(), "No background terminals running.") {
		t.Fatalf("/ps after stop should show empty state:\n%s", model.View())
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
	if !strings.Contains(model.View(), "• done") {
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
	model.Update(ThreadEventMsg{Event: protocol.ToolCallInputDelta("tool-1", "tool-1", "")})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ToolCallItem("tool-1", "exec_command", `{"cmd":"date"}`))})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ToolOutputItemWithCallID("tool-output-1", "tool-1", "exec_command", "Wall time: 0.0100 seconds\nProcess exited with code 0\nOutput:\nok\n", true, map[string]any{
		"call_id":     "tool-1",
		"stdout":      "ok\n",
		"exit_code":   0,
		"duration_ms": 10,
	}))})
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
	cleanView := utils.StripANSI(view)
	for _, want := range []string{"• hello", "Ran date", "ok"} {
		if !strings.Contains(cleanView, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"Tool started: exec_command", "Tool input streaming", "Turn completed"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("View() contains stale event log %q:\n%s", unwanted, view)
		}
	}
	if got := countRole(state.Messages, codextui.RoleAssistant); got != 1 {
		t.Fatalf("assistant message count = %d, want 1; messages=%#v", got, state.Messages)
	}
}

func TestModelStreamsToolInputIntoHistoryCell(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.ToolCallItemWithCallID("fc-1", "call-1", "exec_command", ""))})
	model.Update(ThreadEventMsg{Event: protocol.ToolCallInputDelta("fc-1", "call-1", `{"cmd":"`)})
	model.Update(ThreadEventMsg{Event: protocol.ToolCallInputDelta("fc-1", "call-1", `pwd"}`)})

	view := model.View()
	if !strings.Contains(view, "Running pwd") {
		t.Fatalf("streamed tool input did not update history cell:\n%s", view)
	}
	if strings.Contains(view, "Tool input streaming") || strings.Contains(view, "Tool started:") {
		t.Fatalf("view contains stale tool event log:\n%s", view)
	}
}

func TestModelWaitsForActualExecCommandInput(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.ToolCallItemWithCallID("fc-1", "call-1", "exec_command", ""))})
	view := model.View()
	if strings.Contains(view, "Running exec_command") || strings.Contains(view, "Running shell command") {
		t.Fatalf("empty exec_command input should not render a command cell:\n%s", view)
	}

	model.Update(ThreadEventMsg{Event: protocol.ToolCallInputDelta("fc-1", "call-1", `{"cmd":"`)})
	view = model.View()
	if strings.Contains(view, "Running exec_command") || strings.Contains(view, "Running shell command") || strings.Contains(view, `Running {"cmd":"`) {
		t.Fatalf("partial exec_command JSON should not leak as a command:\n%s", view)
	}

	model.Update(ThreadEventMsg{Event: protocol.ToolCallInputDelta("fc-1", "call-1", `pwd"}`)})
	view = model.View()
	if !strings.Contains(view, "Running pwd") {
		t.Fatalf("complete exec_command input should render the actual command:\n%s", view)
	}
	if strings.Contains(view, "Running exec_command") {
		t.Fatalf("exec_command tool name leaked into running cell:\n%s", view)
	}
}

func TestModelRendersCommandExecutionLifecycle(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.CommandExecutionItem("call-1", "Get-ChildItem test.pdf", "", nil, "in_progress"))})
	view := model.View()
	if !strings.Contains(view, "Running Get-ChildItem test.pdf") {
		t.Fatalf("command execution start should show the actual command:\n%s", view)
	}
	if strings.Contains(view, "exec_command") || strings.Contains(view, "shell command") {
		t.Fatalf("command execution start leaked a generic label:\n%s", view)
	}

	exitCode := 0
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.CommandExecutionItem("call-1", "Get-ChildItem test.pdf", "test.pdf\n", &exitCode, "completed"))})
	view = model.View()
	if !strings.Contains(view, "Ran Get-ChildItem test.pdf") || !strings.Contains(view, "test.pdf") {
		t.Fatalf("command execution completion should update the active cell:\n%s", view)
	}
	if strings.Contains(view, "Running Get-ChildItem") || strings.Contains(view, "shell command") {
		t.Fatalf("command execution completion left a stale running cell:\n%s", view)
	}

	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("message-1", "已获取本机网络信息。")})
	view = model.View()
	if !strings.Contains(view, strings.Repeat("─", 20)) {
		t.Fatalf("assistant output after command should use the Rust final-message separator:\n%s", view)
	}
	if got := countRole(state.Messages, codextui.RoleHistory); got != 2 {
		t.Fatalf("history count = %d, want command cell plus separator; messages=%#v", got, state.Messages)
	}
}

func TestModelRendersMCPToolLifecycleLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	arguments := map[string]any{"label": "A"}

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.MCPToolCallItem(
		"call-mcp",
		"geogebra",
		"geogebra_create_point",
		arguments,
		nil,
		nil,
		"in_progress",
	))})
	view := model.View()
	if !strings.Contains(view, `Calling geogebra.geogebra_create_point({"label":"A"})`) {
		t.Fatalf("MCP start should use the Rust calling cell:\n%s", view)
	}
	if strings.Contains(view, "Running geogebra_create_point") {
		t.Fatalf("MCP start leaked a generic exec cell:\n%s", view)
	}

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.MCPToolCallItem(
		"call-mcp",
		"geogebra",
		"geogebra_create_point",
		arguments,
		&protocol.MCPToolResult{Content: []any{map[string]any{"type": "text", "text": "Point A created"}}},
		nil,
		"completed",
	))})
	view = model.View()
	if !strings.Contains(view, `Called geogebra.geogebra_create_point({"label":"A"})`) || !strings.Contains(view, "Point A created") {
		t.Fatalf("MCP completion should update the Rust calling cell:\n%s", view)
	}
	if strings.Contains(view, "Calling geogebra.geogebra_create_point") || countRole(state.Messages, codextui.RoleHistory) != 1 {
		t.Fatalf("MCP completion left a duplicate or stale cell: messages=%#v\n%s", state.Messages, view)
	}
}

func TestModelRendersMCPStartupProgressLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width:                     100,
		Height:                    24,
		MCPStartupExpectedServers: []string{"alpha", "beta"},
	})
	model.now = func() time.Time { return time.Unix(7, 0) }

	model.Update(MCPStartupUpdateMsg{Name: "alpha", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupStarting}})
	model.Update(MCPStartupUpdateMsg{Name: "beta", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupStarting}})
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "Starting MCP servers (0/2): alpha, beta (0s") {
		t.Fatalf("initial MCP startup header missing:\n%s", view)
	}

	model.Update(MCPStartupUpdateMsg{Name: "alpha", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupReady}})
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "Starting MCP servers (1/2): beta") {
		t.Fatalf("MCP progress header missing:\n%s", view)
	}

	_, cmd := model.Update(MCPStartupUpdateMsg{Name: "beta", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupReady}})
	if !model.mcpStartupActive || !strings.Contains(utils.StripANSI(model.View()), "Starting MCP servers") {
		t.Fatalf("settled MCP startup should stay visible during lag:\n%s", model.View())
	}
	if cmd == nil {
		t.Fatal("completed MCP startup should schedule finish lag")
	}
	model.Update(mcpStartupFinishAfterLagMsg{Generation: model.mcpStartupGeneration})
	if model.mcpStartupActive || strings.Contains(utils.StripANSI(model.View()), "Starting MCP servers") {
		t.Fatalf("completed MCP startup should clear after lag:\n%s", model.View())
	}
}

func TestModelQueuesInputUntilMCPStartupFinishes(t *testing.T) {
	state := codextui.NewState(nil)
	var submitted []string
	model := NewModel(state, Options{
		MCPStartupExpectedServers: []string{"docs"},
		OnSubmit: func(prompt string) bubbletea.Cmd {
			submitted = append(submitted, prompt)
			return nil
		},
	})
	model.Update(MCPStartupUpdateMsg{Name: "docs", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupStarting}})
	typeText(t, model, "queued during startup")
	model.Update(key(bubbletea.KeyEnter))
	if len(submitted) != 0 || len(model.QueuedRequests()) != 1 {
		t.Fatalf("input should remain queued during MCP startup: submitted=%#v queued=%#v", submitted, model.QueuedRequests())
	}

	_, cmd := model.Update(MCPStartupUpdateMsg{Name: "docs", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupReady}})
	if len(submitted) != 0 {
		t.Fatalf("queued input should wait for MCP startup lag: %#v", submitted)
	}
	if cmd == nil {
		t.Fatal("ready update should schedule MCP startup finish lag")
	}
	if model.mcpStartupGeneration == 0 || !model.mcpStartupFinishPending {
		t.Fatalf("finish lag not pending: generation=%d pending=%v", model.mcpStartupGeneration, model.mcpStartupFinishPending)
	}
	model.Update(mcpStartupFinishAfterLagMsg{Generation: model.mcpStartupGeneration})
	if !reflect.DeepEqual(submitted, []string{"queued during startup"}) {
		t.Fatalf("queued input was not released after MCP startup: %#v", submitted)
	}
}

func TestModelMCPStartupFailureWarningsMatchRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{MCPStartupExpectedServers: []string{"alpha", "beta"}})
	model.Update(MCPStartupUpdateMsg{Name: "alpha", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupStarting}})
	model.Update(MCPStartupUpdateMsg{Name: "beta", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupStarting}})
	model.Update(MCPStartupUpdateMsg{Name: "alpha", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupFailed, Error: "alpha handshake failed"}})
	_, cmd := model.Update(MCPStartupUpdateMsg{Name: "beta", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupReady}})

	view := utils.StripANSI(model.View())
	if strings.Contains(view, "MCP startup incomplete (failed: alpha)") {
		t.Fatalf("final MCP startup warning should wait for lag:\n%s", view)
	}
	for _, want := range []string{"alpha handshake failed", "Starting MCP servers"} {
		if !strings.Contains(view, want) {
			t.Fatalf("MCP startup warning missing %q:\n%s", want, view)
		}
	}
	if cmd == nil {
		t.Fatal("settled MCP startup should schedule finish lag")
	}
	model.Update(mcpStartupFinishAfterLagMsg{Generation: model.mcpStartupGeneration})
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "MCP startup incomplete (failed: alpha)") {
		t.Fatalf("final MCP startup warning missing after lag:\n%s", view)
	}
}

func TestModelMCPStartupInterruptIgnoresLateTerminalUpdates(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{MCPStartupExpectedServers: []string{"alpha", "beta"}})
	model.Update(MCPStartupUpdateMsg{Name: "alpha", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupStarting}})
	model.Update(MCPStartupUpdateMsg{Name: "beta", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupStarting}})
	model.Update(MCPStartupFinishAfterLagMsg{})
	if model.mcpStartupActive {
		t.Fatal("MCP startup remained active after interruption")
	}

	model.Update(MCPStartupUpdateMsg{Name: "alpha", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupReady}})
	model.Update(MCPStartupUpdateMsg{Name: "beta", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupReady}})
	if model.mcpStartupActive || strings.Contains(utils.StripANSI(model.View()), "Starting MCP servers") {
		t.Fatalf("late terminal updates reopened MCP startup:\n%s", model.View())
	}
}

func TestModelStreamsUpdatePlanIntoPlanCell(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.ToolCallItemWithCallID("plan-item", "plan-call", "update_plan", ""))})
	model.Update(ThreadEventMsg{Event: protocol.ToolCallInputDelta("plan-item", "plan-call", `{"plan":[{"step":"read Rust TUI","status":"in_progress"},`)})
	if strings.Contains(model.View(), "Running update_plan") || strings.Contains(model.View(), "Ran update_plan") {
		t.Fatalf("partial update_plan JSON should not render as exec cell:\n%s", model.View())
	}
	model.Update(ThreadEventMsg{Event: protocol.ToolCallInputDelta("plan-item", "plan-call", `{"step":"port behavior","status":"pending"}],"explanation":"align with Rust"}`)})

	view := model.View()
	for _, want := range []string{"Updated Plan", "align with Rust", "read Rust TUI", "port behavior"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"Running update_plan", "Ran update_plan"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("update_plan rendered as exec cell %q:\n%s", unwanted, view)
		}
	}
}

func TestModelDoesNotMarkUpdatePlanFailedOnTurnFailure(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.ToolCallItemWithCallID("plan-item", "plan-call", "update_plan", ""))})
	model.Update(ThreadEventMsg{Event: protocol.ToolCallInputDelta("plan-item", "plan-call", `{"plan":[{"step":"read Rust TUI","status":"completed"},{"step":"port behavior","status":"in_progress"}]}`)})
	model.Update(ThreadEventMsg{Event: protocol.TurnFailed("network boom")})

	view := model.View()
	for _, want := range []string{"Updated Plan", "read Rust TUI", "port behavior", "Error: network boom"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"Running update_plan", "Ran update_plan", "update_plan {", "Error: network boom\n  └"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("update_plan should not be rendered or failed as exec cell %q:\n%s", unwanted, view)
		}
	}
}

func TestModelFailsRunningToolCellsAndClearsUnconfirmedThread(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ThreadEventMsg{Event: protocol.ThreadStarted("thread-new")})
	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.ToolCallItemWithCallID("fc-1", "call-1", "exec_command", ""))})
	model.Update(ThreadEventMsg{Event: protocol.ToolCallInputDelta("fc-1", "call-1", `{"cmd":"pwd"}`)})
	model.Update(ThreadEventMsg{Event: protocol.TurnFailed("network boom")})

	if state.ThreadID != "" {
		t.Fatalf("ThreadID after failed first turn = %q, want cleared", state.ThreadID)
	}
	if state.Status != "error" {
		t.Fatalf("Status = %q, want error", state.Status)
	}
	view := model.View()
	for _, want := range []string{"Ran pwd", "Error: network boom"} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Running pwd") || strings.Contains(view, "System:") {
		t.Fatalf("View() kept stale running/system error output:\n%s", view)
	}
}

func TestModelKeepsConfirmedThreadOnTransientTurnFailure(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-existing")
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.Update(ThreadEventMsg{Event: protocol.TurnFailed("read tcp: connection reset")})

	if state.ThreadID != "thread-existing" {
		t.Fatalf("ThreadID after transient failure = %q, want existing thread", state.ThreadID)
	}
	if !strings.Contains(model.View(), "Error: read tcp: connection reset") {
		t.Fatalf("View() missing transient error:\n%s", model.View())
	}
}

func TestModelClearsThreadNotFoundFailures(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-missing")
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(TurnCompletedMsg{Err: errors.New("thread not found: thread-missing")})

	if state.ThreadID != "" {
		t.Fatalf("ThreadID after thread-not-found = %q, want cleared", state.ThreadID)
	}
	if !strings.Contains(model.View(), "Error: thread not found: thread-missing") {
		t.Fatalf("View() missing thread-not-found error:\n%s", model.View())
	}
}

func TestModelDedupesRepeatedTurnErrors(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.Update(ThreadEventMsg{Event: protocol.ErrorEvent("network boom")})
	model.Update(ThreadEventMsg{Event: protocol.TurnFailed("network boom")})
	model.Update(TurnCompletedMsg{Err: errors.New("network boom")})

	count := 0
	for _, message := range state.Messages {
		if strings.Contains(message.Text, "Error: network boom") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("error history count = %d, want 1; messages=%#v", count, state.Messages)
	}
}

func TestModelAppliesRateLimitWarnings(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	fiveHours := int64(5 * 60)

	model.Update(RateLimitSnapshotMsg{Snapshot: chatwidget.RateLimitSnapshot{
		LimitID: "codex",
		Primary: &chatwidget.RateLimitWindow{
			UsedPercent:        74,
			WindowDurationMins: &fiveHours,
		},
	}})
	if got := countRole(state.Messages, codextui.RoleHistory); got != 0 {
		t.Fatalf("history warning count = %d, want 0; messages=%#v", got, state.Messages)
	}

	model.Update(RateLimitSnapshotMsg{Snapshot: chatwidget.RateLimitSnapshot{
		LimitID: "codex",
		Primary: &chatwidget.RateLimitWindow{
			UsedPercent:        90,
			WindowDurationMins: &fiveHours,
		},
	}})
	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("history warning count = %d, want 1; messages=%#v", got, state.Messages)
	}
	if !strings.Contains(state.Messages[0].RawText, "less than 10% of your 5h limit left") {
		t.Fatalf("warning raw text = %q", state.Messages[0].RawText)
	}

	model.Update(RateLimitSnapshotMsg{Snapshot: chatwidget.RateLimitSnapshot{
		LimitID: "codex",
		Primary: &chatwidget.RateLimitWindow{
			UsedPercent:        91,
			WindowDurationMins: &fiveHours,
		},
	}})
	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("history warning repeated count = %d, want 1; messages=%#v", got, state.Messages)
	}

	model.Update(RateLimitSnapshotMsg{Snapshot: chatwidget.RateLimitSnapshot{
		LimitID: "codex",
		Primary: &chatwidget.RateLimitWindow{
			UsedPercent:        96,
			WindowDurationMins: &fiveHours,
		},
	}})
	if got := countRole(state.Messages, codextui.RoleHistory); got != 2 {
		t.Fatalf("history warning count = %d, want 2; messages=%#v", got, state.Messages)
	}
	if !strings.Contains(state.Messages[1].RawText, "less than 5% of your 5h limit left") {
		t.Fatalf("second warning raw text = %q", state.Messages[1].RawText)
	}
}

func TestModelAppliesRateLimitProtocolEvent(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	weekly := int64(7 * 24 * 60)

	model.Update(ThreadEventMsg{Event: protocol.RateLimitSnapshotEvent(protocol.RateLimitSnapshot{
		LimitID: "codex",
		Secondary: &protocol.RateLimitWindow{
			UsedPercent:        75,
			WindowDurationMins: &weekly,
		},
	})})

	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("history warning count = %d, want 1; messages=%#v", got, state.Messages)
	}
	if !strings.Contains(state.Messages[0].RawText, "less than 25% of your weekly limit left") {
		t.Fatalf("warning raw text = %q", state.Messages[0].RawText)
	}
}

func TestModelRateLimitSwitchPromptSwitchesModel(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.4", ReasoningEffort: "medium"})
	var responses []ModalResponse
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		ModelPickerOptions: []codextui.ModelPickerOption{
			{ID: "gpt-5.4", Label: "GPT 5.4"},
			{ID: chatwidget.NudgeModelSlug, Label: "Mini", Description: "Small, fast, and cost-efficient model for simpler coding tasks.", DefaultReasoningEffort: "low"},
		},
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})

	model.Update(RateLimitSnapshotMsg{Snapshot: chatwidget.RateLimitSnapshot{
		LimitID: "codex",
		Primary: &chatwidget.RateLimitWindow{
			UsedPercent: 90,
		},
	}})
	view := model.View()
	for _, want := range []string{"Approaching rate limits", "Switch to " + chatwidget.NudgeModelSlug, "Keep current model (never show again)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("rate-limit switch prompt missing %q:\n%s", want, view)
		}
	}

	model.Update(key(bubbletea.KeyEnter))
	if state.Model != chatwidget.NudgeModelSlug || state.ReasoningEffort != "low" {
		t.Fatalf("state model=%q reasoning=%q", state.Model, state.ReasoningEffort)
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != "rate_limit_switch_model" || responses[0].Picker.Value != chatwidget.NudgeModelSlug {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestModelRateLimitSwitchPromptHidePersistsNotice(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.4"})
	var writes [][]SettingsEdit
	hidden := true
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		ModelPickerOptions: []codextui.ModelPickerOption{
			{ID: chatwidget.NudgeModelSlug, Label: "Mini", DefaultReasoningEffort: "low"},
		},
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			return SettingsWriteResult{HideRateLimitModelNudge: &hidden, FilePath: `D:\codex\config.toml`}, nil
		},
	})

	model.Update(RateLimitSnapshotMsg{Snapshot: chatwidget.RateLimitSnapshot{
		LimitID: "codex",
		Primary: &chatwidget.RateLimitWindow{
			UsedPercent: 90,
		},
	}})
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyDown))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(writes) != 1 || len(writes[0]) != 1 || writes[0][0].KeyPath != "notices.hide_rate_limit_model_nudge" || writes[0][0].Value != true {
		t.Fatalf("writes = %#v", writes)
	}
	if !strings.Contains(model.View(), "Rate limit model switch reminders hidden. Saved to") {
		t.Fatalf("hide notice missing:\n%s", model.View())
	}

	model.Update(RateLimitSnapshotMsg{Snapshot: chatwidget.RateLimitSnapshot{
		LimitID: "codex",
		Primary: &chatwidget.RateLimitWindow{
			UsedPercent: 95,
		},
	}})
	if strings.Contains(model.View(), "Approaching rate limits") {
		t.Fatalf("hidden prompt should not reopen:\n%s", model.View())
	}
}

func TestModelStatusWarningUsesWarningDisplayState(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	warning := "Model metadata for `gpt-test` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."

	model.Update(StatusMsg{Status: "warning: " + warning})
	model.Update(StatusMsg{Status: "warning: " + warning})
	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("deduped warning count = %d, want 1; messages=%#v", got, state.Messages)
	}
	if !strings.Contains(state.Messages[0].RawText, "Model metadata for `gpt-test` not found") {
		t.Fatalf("warning raw text = %q", state.Messages[0].RawText)
	}
	if state.Status != "warning" {
		t.Fatalf("status = %q, want warning", state.Status)
	}

	model.Update(StatusMsg{Status: "warning: plain warning"})
	model.Update(StatusMsg{Status: "warning: plain warning"})
	if got := countRole(state.Messages, codextui.RoleHistory); got != 3 {
		t.Fatalf("plain warning count = %d, want 3; messages=%#v", got, state.Messages)
	}
}

func TestModelAppliesHookRunMessages(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(HookRunMsg{
		ThreadID:      "thread-hook",
		EventName:     "preToolUse",
		Status:        "running",
		StatusMessage: "checking command",
		Running:       true,
	})
	view := model.View()
	if state.ThreadID != "thread-hook" {
		t.Fatalf("ThreadID = %q, want thread-hook", state.ThreadID)
	}
	if !strings.Contains(view, "Running PreToolUse hook: checking command") {
		t.Fatalf("View() missing running hook:\n%s", view)
	}

	model.Update(HookRunMsg{
		ThreadID:  "thread-hook",
		EventName: "postToolUse",
		Status:    "failed",
		Entries: []HookOutputEntry{
			{Kind: "warning", Text: "Heads up from the hook"},
			{Kind: "context", Text: "Remember the startup checklist."},
			{Kind: "error", Text: "hook exited with code 7"},
		},
	})
	view = model.View()
	for _, want := range []string{
		"PostToolUse hook (failed)",
		"warning: Heads up from the hook",
		"hook context: Remember the startup checklist.",
		"error: hook exited with code 7",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("View() missing %q:\n%s", want, view)
		}
	}
	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("history message count = %d, want 1; messages=%#v", got, state.Messages)
	}
}

func TestModelHooksCommandReadsRuntimeHooks(t *testing.T) {
	state := codextui.NewState(nil)
	calls := 0
	model := NewModel(state, Options{
		Width:            80,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnReadHooks: func(cwd string) ([]chatwidget.HookRun, error) {
			calls++
			if cwd != `D:\repo` {
				t.Fatalf("cwd = %q, want D:\\repo", cwd)
			}
			return []chatwidget.HookRun{{
				ID:      "hook-1",
				Name:    "preToolUse / Bash",
				Command: "echo ok",
				Issue:   "source: project\ntrust: trusted",
				Managed: true,
			}}, nil
		},
	})

	typeText(t, model, "/hooks")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/hooks did not return hooks reader command")
	}
	runTeaCmd(t, model, cmd)
	if calls != 1 {
		t.Fatalf("hooks reader calls = %d, want 1", calls)
	}
	view := model.View()
	for _, want := range []string{"Hooks", "preToolUse / Bash", "echo ok", "source: project", "trust: trusted", "managed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("hooks view missing %q:\n%s", want, view)
		}
	}
}

func TestModelHooksCommandUsesLifecycleFallback(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(HookRunMsg{
		ID:            "run-1",
		EventName:     "preToolUse",
		Status:        "running",
		StatusMessage: "checking command",
		Running:       true,
	})
	model.Update(HookRunMsg{
		ID:            "run-1",
		EventName:     "preToolUse",
		Status:        "failed",
		StatusMessage: "hook exited with code 7",
	})

	typeText(t, model, "/hooks")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"Hooks", "preToolUse", "(failed)", "hook exited with code 7"} {
		if !strings.Contains(view, want) {
			t.Fatalf("lifecycle hooks view missing %q:\n%s", want, view)
		}
	}
}

func TestModelPluginsCommandReadsRuntimeCatalog(t *testing.T) {
	state := codextui.NewState(nil)
	calls := 0
	display := "Docs"
	short := "Search docs."
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnReadPlugins: func() (plugin.PluginListResponse, error) {
			calls++
			return plugin.PluginListResponse{Marketplaces: []plugin.PluginMarketplaceEntry{{
				Name: "team",
				Plugins: []plugin.PluginSummary{{
					ID:            "docs@team",
					Name:          "docs",
					Availability:  plugin.PluginAvailable,
					InstallPolicy: plugin.InstallAllowed,
					Installed:     true,
					Enabled:       true,
					Interface: &plugin.PluginInterface{
						DisplayName:      &display,
						ShortDescription: &short,
					},
				}},
			}}}, nil
		},
	})

	typeText(t, model, "/plugins")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/plugins did not return plugin reader command")
	}
	if !strings.Contains(model.View(), "Loading plugins") {
		t.Fatalf("plugins loading view missing:\n%s", model.View())
	}
	runTeaCmd(t, model, cmd)
	if calls != 1 {
		t.Fatalf("plugin reader calls = %d, want 1", calls)
	}
	view := model.View()
	for _, want := range []string{"Plugins", "Docs", "Installed", "Search docs.", "Installed 1 of 1 available plugins."} {
		if !strings.Contains(view, want) {
			t.Fatalf("plugins view missing %q:\n%s", want, view)
		}
	}
}

func TestModelAppsCommandReadsRuntimeCatalog(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-apps")
	calls := 0
	desc := "Search Drive files."
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnReadApps: func(threadID string, forceRefetch bool) (appsapi.AppListResponse, error) {
			calls++
			if threadID != "thread-apps" || !forceRefetch {
				t.Fatalf("apps reader got threadID=%q forceRefetch=%v", threadID, forceRefetch)
			}
			return appsapi.AppListResponse{Data: []appsapi.AppEntry{{
				ID:           "drive",
				Name:         "Google Drive",
				Description:  &desc,
				IsAccessible: true,
				IsEnabled:    true,
			}}}, nil
		},
	})

	typeText(t, model, "/apps")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/apps did not return app reader command")
	}
	if !strings.Contains(model.View(), "Loading installed and available apps") {
		t.Fatalf("apps loading view missing:\n%s", model.View())
	}
	runTeaCmd(t, model, cmd)
	if calls != 1 {
		t.Fatalf("apps reader calls = %d, want 1", calls)
	}
	view := model.View()
	for _, want := range []string{"Apps", "Google Drive", "Search Drive files.", "Installed 1 of 1 available apps."} {
		if !strings.Contains(view, want) {
			t.Fatalf("apps view missing %q:\n%s", want, view)
		}
	}
}

func TestModelReviewCustomStartsRuntimeReview(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	var captured review.StartParams
	calls := 0
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartReview: func(params review.StartParams) (review.StartResponse, error) {
			calls++
			captured = params
			return review.StartResponse{
				Turn:           review.Turn{ID: "review-turn", Status: review.TurnStatusInProgress},
				ReviewThreadID: "thread-review",
			}, nil
		},
	})

	typeText(t, model, "/review check security")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/review custom did not return review/start command")
	}
	runTeaCmd(t, model, cmd)
	if calls != 1 {
		t.Fatalf("review starter calls = %d, want 1", calls)
	}
	if captured.ThreadID != "thread-review" || captured.Delivery == nil || *captured.Delivery != "inline" {
		t.Fatalf("review params = %#v", captured)
	}
	if captured.Target.Type != "custom" || captured.Target.Instructions != "check security" {
		t.Fatalf("review target = %#v", captured.Target)
	}
	view := model.View()
	for _, want := range []string{"Review started.", "target: custom instructions", "turn: review-turn"} {
		if !strings.Contains(view, want) {
			t.Fatalf("review view missing %q:\n%s", want, view)
		}
	}
}

func TestModelSideCommandStartsRuntimeSideConversation(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5", ReasoningEffort: "high"})
	state.SetThreadID("thread-parent")
	state.AddMessage(codextui.RoleUser, "main question")
	var captured SideStartParams
	calls := 0
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			calls++
			captured = params
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
	})

	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/side did not return side start command")
	}
	runTeaCmd(t, model, cmd)
	if calls != 1 {
		t.Fatalf("side starter calls = %d, want 1", calls)
	}
	if captured.CommandName != "/side" || captured.ParentThreadID != "thread-parent" || captured.UserMessage != "" {
		t.Fatalf("side params = %#v", captured)
	}
	if state.ThreadID != "thread-side" {
		t.Fatalf("thread id after side start = %q", state.ThreadID)
	}
	if len(state.Messages) != 0 {
		t.Fatalf("side transcript should start empty, got %#v", state.Messages)
	}
	if view := model.View(); !strings.Contains(view, "Side from main thread") || !strings.Contains(view, "Ctrl+C to return") {
		t.Fatalf("side context missing:\n%s", view)
	}

	model.Update(key(bubbletea.KeyCtrlC))
	if state.ThreadID != "thread-parent" {
		t.Fatalf("thread id after side return = %q", state.ThreadID)
	}
	if len(state.Messages) != 1 || state.Messages[0].Text != "main question" {
		t.Fatalf("parent transcript not restored: %#v", state.Messages)
	}
}

func TestModelSideReturnClosesRuntimeSideConversation(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	state.AddMessage(codextui.RoleUser, "main question")
	var closed SideCloseParams
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
		OnCloseSide: func(params SideCloseParams) (SideCloseResponse, error) {
			closed = params
			return SideCloseResponse{}, nil
		},
	})

	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	_, cmd = model.Update(key(bubbletea.KeyCtrlC))
	if cmd == nil {
		t.Fatal("side Ctrl+C did not return close command")
	}
	if state.ThreadID != "thread-side" {
		t.Fatalf("thread id before close result = %q, want side", state.ThreadID)
	}
	runTeaCmd(t, model, cmd)

	if closed.ParentThreadID != "thread-parent" || closed.SideThreadID != "thread-side" {
		t.Fatalf("closed side params = %#v", closed)
	}
	if state.ThreadID != "thread-parent" {
		t.Fatalf("thread id after side close = %q", state.ThreadID)
	}
}

func TestModelSideCloseFailureKeepsSideVisible(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
		OnCloseSide: func(params SideCloseParams) (SideCloseResponse, error) {
			return SideCloseResponse{}, errors.New("transport closed")
		},
	})

	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	_, cmd = model.Update(key(bubbletea.KeyCtrlC))
	runTeaCmd(t, model, cmd)

	if state.ThreadID != "thread-side" {
		t.Fatalf("thread id after failed side close = %q, want side", state.ThreadID)
	}
	if view := model.View(); !strings.Contains(view, "Failed to close side conversation thread-side") {
		t.Fatalf("missing close failure:\n%s", view)
	}
}

func TestModelSideParentStatusUpdatesFooter(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
	})

	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	model.Update(SideParentStatusChangeMsg{
		ParentThreadID: "thread-parent",
		Kind:           SideParentStatusChangeSet,
		Status:         SideParentStatusNeedsApproval,
	})
	if view := model.View(); !strings.Contains(view, "main needs approval") {
		t.Fatalf("side footer missing parent status:\n%s", view)
	}
	model.Update(SideParentStatusChangeMsg{
		ParentThreadID: "thread-parent",
		Kind:           SideParentStatusChangeClearActionable,
	})
	if view := model.View(); strings.Contains(view, "main needs approval") {
		t.Fatalf("side footer did not clear actionable status:\n%s", view)
	}
}

func TestModelSideKeepsParentTranscriptSnapshotUpdated(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	state.AddMessage(codextui.RoleUser, "main question")
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
	})

	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	model.Update(ThreadScopedEventMsg{
		ThreadID: "thread-parent",
		Event:    protocol.AgentMessageDelta("item-parent", "background answer"),
	})
	if view := model.View(); strings.Contains(view, "background answer") {
		t.Fatalf("parent transcript leaked into side view:\n%s", view)
	}

	model.Update(key(bubbletea.KeyCtrlC))
	if state.ThreadID != "thread-parent" {
		t.Fatalf("thread id after side return = %q", state.ThreadID)
	}
	if view := model.View(); !strings.Contains(view, "background answer") {
		t.Fatalf("parent transcript snapshot was not restored:\n%s", view)
	}
}

func TestModelSideRejectsUnavailableSlashCommands(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
	})

	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	typeText(t, model, "/review")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd != nil {
		t.Fatalf("side /review returned cmd %#v", cmd)
	}
	if view := model.View(); !strings.Contains(view, "'/review' is unavailable in side conversations") {
		t.Fatalf("missing side slash rejection:\n%s", view)
	}
}

func TestModelSideCommandStartsWhileParentTaskRunning(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	state.SetStatus("running")
	calls := 0
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			calls++
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
	})

	typeText(t, model, "/side inspect")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("running /side did not return side start command")
	}
	runTeaCmd(t, model, cmd)

	if calls != 1 {
		t.Fatalf("side starter calls = %d, want 1", calls)
	}
	if got := model.QueuedRequests(); len(got) != 0 {
		t.Fatalf("running /side should not queue, got %#v", got)
	}
	if state.ThreadID != "thread-side" {
		t.Fatalf("thread after running /side = %q", state.ThreadID)
	}
}

func TestModelBtwCommandStartsSideWithPlainUserTurn(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5", ReasoningEffort: "high"})
	state.SetThreadID("thread-parent")
	var captured SideStartParams
	var submitted []SubmitRequest
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			captured = params
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})

	typeText(t, model, "/btw !echo hello")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)

	if captured.CommandName != "/btw" || captured.UserMessage != "!echo hello" {
		t.Fatalf("side params = %#v", captured)
	}
	if len(submitted) != 1 || submitted[0].Prompt != "!echo hello" {
		t.Fatalf("submitted side request = %#v", submitted)
	}
	if state.ThreadID != "thread-side" || state.Status != "running" {
		t.Fatalf("side state thread=%q status=%q", state.ThreadID, state.Status)
	}
}

func TestModelSideCommandRequiresStartedThread(t *testing.T) {
	state := codextui.NewState(nil)
	calls := 0
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			calls++
			return SideStartResponse{}, nil
		},
	})

	typeText(t, model, "/side explore")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd != nil {
		t.Fatalf("/side without thread returned cmd %#v", cmd)
	}
	if calls != 0 {
		t.Fatalf("side starter called %d times", calls)
	}
	if len(state.Messages) == 0 || !strings.Contains(state.Messages[0].RawText, SideNoStartedConversationMessage) {
		t.Fatalf("missing no-started-thread message: %#v", state.Messages)
	}
}

func TestModelSideStartErrorMapsMissingConversation(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			return SideStartResponse{}, errors.New("thread/fork failed: no rollout found for thread id thread-parent")
		},
	})

	typeText(t, model, "/side explore")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if view := model.View(); !strings.Contains(view, SideNoStartedConversationMessage) {
		t.Fatalf("missing mapped side error:\n%s", view)
	}
}

func TestModelReviewUncommittedPresetStartsRuntimeReview(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	var captured review.StartParams
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnStartReview: func(params review.StartParams) (review.StartResponse, error) {
			captured = params
			return review.StartResponse{Turn: review.Turn{ID: "review-turn"}, ReviewThreadID: "thread-review"}, nil
		},
	})

	typeText(t, model, "/review")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyDown))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/review uncommitted did not return review/start command")
	}
	runTeaCmd(t, model, cmd)
	if captured.ThreadID != "thread-review" || captured.Target.Type != "uncommittedChanges" {
		t.Fatalf("review params = %#v", captured)
	}
}

func TestModelReviewBranchPickerStartsRuntimeReview(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	var captured review.StartParams
	model := NewModel(state, Options{
		Width:            80,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnReadReviewBranches: func(cwd string) (string, []string, error) {
			if cwd != `D:\repo` {
				t.Fatalf("branch reader cwd = %q", cwd)
			}
			return "feature", []string{"main", "release"}, nil
		},
		OnStartReview: func(params review.StartParams) (review.StartResponse, error) {
			captured = params
			return review.StartResponse{Turn: review.Turn{ID: "review-turn"}, ReviewThreadID: "thread-review"}, nil
		},
	})

	typeText(t, model, "/review")
	model.Update(key(bubbletea.KeyEnter))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("review branch picker did not return branch reader command")
	}
	runTeaCmd(t, model, cmd)
	if view := model.View(); !strings.Contains(view, "Select a base branch") || !strings.Contains(view, "feature -> main") {
		t.Fatalf("branch picker missing:\n%s", view)
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("review branch selection did not return review/start command")
	}
	runTeaCmd(t, model, cmd)
	if captured.Target.Type != "baseBranch" || captured.Target.Branch != "main" {
		t.Fatalf("review branch target = %#v", captured.Target)
	}
}

func TestModelReviewCommitPickerStartsRuntimeReview(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	var captured review.StartParams
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnReadReviewCommits: func(cwd string, limit int) ([]chatwidget.ReviewCommitEntry, error) {
			if limit != 100 {
				t.Fatalf("commit reader limit = %d", limit)
			}
			return []chatwidget.ReviewCommitEntry{{Subject: "Fix auth", SHA: "abc123"}}, nil
		},
		OnStartReview: func(params review.StartParams) (review.StartResponse, error) {
			captured = params
			return review.StartResponse{Turn: review.Turn{ID: "review-turn"}, ReviewThreadID: "thread-review"}, nil
		},
	})

	typeText(t, model, "/review")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyDown))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("review commit picker did not return commit reader command")
	}
	runTeaCmd(t, model, cmd)
	if view := model.View(); !strings.Contains(view, "Select a commit to review") || !strings.Contains(view, "Fix auth") {
		t.Fatalf("commit picker missing:\n%s", view)
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("review commit selection did not return review/start command")
	}
	runTeaCmd(t, model, cmd)
	if captured.Target.Type != "commit" || captured.Target.SHA != "abc123" || captured.Target.Title == nil || *captured.Target.Title != "Fix auth" {
		t.Fatalf("review commit target = %#v", captured.Target)
	}
}

func TestModelSkillsManageReadsRuntimeInventory(t *testing.T) {
	state := codextui.NewState(nil)
	calls := 0
	model := NewModel(state, Options{
		Width:            80,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnReadSkills: func(cwd string) (appserver.SkillsListResponse, error) {
			calls++
			if cwd != `D:\repo` {
				t.Fatalf("skills cwd = %q, want D:\\repo", cwd)
			}
			return appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{{
				CWD: `D:\repo`,
				Skills: []appserver.SkillsListEntry{{
					Name:             "Docs:review",
					Path:             `D:\repo\.codex\skills\review\SKILL.md`,
					Scope:            "plugin",
					ShortDescription: "Review code",
					Enabled:          true,
					PluginID:         "docs@team",
				}},
			}}}, nil
		},
	})

	typeText(t, model, "/skills")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyDown))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/skills manage did not return skills reader command")
	}
	if !strings.Contains(model.View(), "Loading skills") {
		t.Fatalf("skills loading view missing:\n%s", model.View())
	}
	runTeaCmd(t, model, cmd)
	if calls != 1 {
		t.Fatalf("skills reader calls = %d, want 1", calls)
	}
	view := model.View()
	for _, want := range []string{"Skills", "review (Docs)", "Review code", "1 enabled of 1 skills."} {
		if !strings.Contains(view, want) {
			t.Fatalf("skills view missing %q:\n%s", want, view)
		}
	}
}

func TestModelSkillPopupReadsRuntimeInventoryAndInsertsSkill(t *testing.T) {
	state := codextui.NewState(nil)
	calls := 0
	model := NewModel(state, Options{
		Width:            90,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnReadSkills: func(cwd string) (appserver.SkillsListResponse, error) {
			calls++
			if cwd != `D:\repo` {
				t.Fatalf("skills cwd = %q, want D:\\repo", cwd)
			}
			return appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{{
				CWD: `D:\repo`,
				Skills: []appserver.SkillsListEntry{{
					Name:             "imagegen",
					Path:             `C:\Users\me\.codex\skills\.system\imagegen\SKILL.md`,
					Scope:            "system",
					Description:      "Generate or edit images for websites, games, and more",
					Enabled:          true,
					ShortDescription: "Generate or edit images for websites, games, and more",
					Interface: &appserver.SkillInterface{
						DisplayName: "Image Gen",
					},
				}},
			}}}, nil
		},
	})

	_, cmd := model.Update(runes("$"))
	if !strings.Contains(model.View(), "Loading skills") {
		t.Fatalf("skill popup loading view missing:\n%s", model.View())
	}
	runTeaCmd(t, model, cmd)
	if calls != 1 {
		t.Fatalf("skills reader calls = %d, want 1", calls)
	}
	view := model.View()
	for _, want := range []string{"Image Gen", "Generate or edit images"} {
		if !strings.Contains(view, want) {
			t.Fatalf("skill popup missing %q:\n%s", want, view)
		}
	}

	model.Update(key(bubbletea.KeyEnter))
	if got := model.ComposerValue(); got != "$imagegen " {
		t.Fatalf("composer = %q, want $imagegen space", got)
	}
}

func TestModelSkillPopupSubmissionCarriesMentionBindingAndCatalog(t *testing.T) {
	var requests []SubmitRequest
	model := NewModel(codextui.NewState(nil), Options{
		Width:            90,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnReadSkills: func(cwd string) (appserver.SkillsListResponse, error) {
			return appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{{
				CWD: `D:\repo`,
				Skills: []appserver.SkillsListEntry{{
					Name:             "imagegen",
					Path:             `C:\Users\me\.codex\skills\.system\imagegen\SKILL.md`,
					Scope:            "system",
					Description:      "Generate images",
					Enabled:          true,
					ShortDescription: "Generate images",
					Interface:        &appserver.SkillInterface{DisplayName: "Image Gen"},
				}},
			}}}, nil
		},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})

	_, cmd := model.Update(runes("$"))
	runTeaCmd(t, model, cmd)
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyEnter))

	if len(requests) != 1 {
		t.Fatalf("requests = %#v", requests)
	}
	if requests[0].Prompt != "$imagegen" {
		t.Fatalf("prompt = %q, want $imagegen", requests[0].Prompt)
	}
	wantBinding := `imagegen|skill://C:\Users\me\.codex\skills\.system\imagegen\SKILL.md`
	if len(requests[0].MentionBindings) != 1 || requests[0].MentionBindings[0] != wantBinding {
		t.Fatalf("mention bindings = %#v, want %q", requests[0].MentionBindings, wantBinding)
	}
	if len(requests[0].MentionCatalog.Skills) != 1 || requests[0].MentionCatalog.Skills[0].Name != "imagegen" {
		t.Fatalf("mention catalog = %#v", requests[0].MentionCatalog)
	}
}

func TestModelSkillsListMenuOpensSkillPopup(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:            90,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnReadSkills: func(cwd string) (appserver.SkillsListResponse, error) {
			return appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{{
				CWD: `D:\repo`,
				Skills: []appserver.SkillsListEntry{{
					Name:        "openai-docs",
					Path:        `C:\Users\me\.codex\skills\.system\openai-docs\SKILL.md`,
					Scope:       "system",
					Description: "Reference OpenAI docs",
					Enabled:     true,
					Interface:   &appserver.SkillInterface{DisplayName: "OpenAI Docs"},
				}},
			}}}, nil
		},
	})

	typeText(t, model, "/skills")
	model.Update(key(bubbletea.KeyEnter))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/skills list did not request skills inventory")
	}
	if got := model.ComposerValue(); got != "$" {
		t.Fatalf("composer = %q, want $", got)
	}
	runTeaCmd(t, model, cmd)
	view := model.View()
	for _, want := range []string{"OpenAI Docs", "Reference OpenAI docs"} {
		if !strings.Contains(view, want) {
			t.Fatalf("/skills list popup missing %q:\n%s", want, view)
		}
	}
}

func TestModelAppliesHistoryCellMessages(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(HistoryCellMsg{Cell: historycell.NewInfoEvent("Background task finished", "open /ps")})
	view := model.View()
	if !strings.Contains(view, "• Background task finished open /ps") {
		t.Fatalf("View() missing history cell:\n%s", view)
	}
	if strings.Contains(view, "History:") {
		t.Fatalf("View() rendered history role header:\n%s", view)
	}
	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("history message count = %d, want 1; messages=%#v", got, state.Messages)
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

func TestModelDeduplicatesNonAdjacentAssistantFinalInCurrentTurn(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{})
	state.AddMessage(codextui.RoleUser, "$openai-docs")

	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-skill", "Using the openai-docs skill because you invoked it.")})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ToolOutputItem("tool-output-1", "exec_command", "skill contents", true))})
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-ready", "Ready - I'll use official OpenAI docs/manual sources.")})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.AgentMessageItem("msg-skill", "Using the openai-docs skill because you invoked it."))})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.AgentMessageItem("msg-ready", "Ready - I'll use official OpenAI docs/manual sources."))})
	model.Update(TurnCompletedMsg{AssistantMessage: "Ready - I'll use official OpenAI docs/manual sources."})

	if got := countRole(state.Messages, codextui.RoleAssistant); got != 2 {
		t.Fatalf("assistant message count = %d, want 2; messages=%#v", got, state.Messages)
	}
	if got := countMessageText(state.Messages, codextui.RoleAssistant, "Using the openai-docs skill because you invoked it."); got != 1 {
		t.Fatalf("skill assistant message count = %d, want 1; messages=%#v", got, state.Messages)
	}
	if got := countMessageText(state.Messages, codextui.RoleAssistant, "Ready - I'll use official OpenAI docs/manual sources."); got != 1 {
		t.Fatalf("ready assistant message count = %d, want 1; messages=%#v", got, state.Messages)
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
						map[string]any{"const": "accept", "title": "Allow once"},
						map[string]any{"const": "accept_session", "title": "Allow session"},
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
	if got.ID != "elicitation-1" || got.Kind != ModalKindElicitation || got.OptionID != "accept_session" || got.Cancelled {
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
	state.SetThreadID("thread-before-clear")
	state.SetStatus("running")
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
	if state.ThreadID != "" || state.Status != "idle" {
		t.Fatalf("clear state = thread %q status %q, want fresh idle session", state.ThreadID, state.Status)
	}
	if !strings.Contains(model.View(), "No messages yet.") {
		t.Fatalf("View() missing empty transcript:\n%s", model.View())
	}
}

func TestModelInputHistoryUpDownRestoresDraft(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	for _, prompt := range []string{"first prompt", "second prompt"} {
		typeText(t, model, prompt)
		model.Update(key(bubbletea.KeyEnter))
	}
	typeText(t, model, "draft")
	model.Update(key(bubbletea.KeyUp))
	if got := model.ComposerValue(); got != "second prompt" {
		t.Fatalf("first up = %q", got)
	}
	model.Update(key(bubbletea.KeyUp))
	if got := model.ComposerValue(); got != "first prompt" {
		t.Fatalf("second up = %q", got)
	}
	model.Update(key(bubbletea.KeyDown))
	if got := model.ComposerValue(); got != "second prompt" {
		t.Fatalf("first down = %q", got)
	}
	model.Update(key(bubbletea.KeyDown))
	if got := model.ComposerValue(); got != "draft" {
		t.Fatalf("restored draft = %q", got)
	}
}

func TestModelVisibleSlashCommandsProduceUserVisibleResult(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-visible")
	state.AddMessage(codextui.RoleAssistant, "last response")
	model := NewModel(state, Options{Width: 100, Height: 30})

	skip := map[string]bool{
		"exit": true,
		"quit": true,
	}
	for _, frame := range codextui.SlashCommandFrames() {
		if slashPopupHiddenCommand(frame.Name) || skip[frame.Name] {
			continue
		}
		t.Run(frame.Name, func(t *testing.T) {
			beforeView := utils.StripANSI(model.View())
			beforeNotice := model.notice
			beforeModal := model.modal
			beforeSubmitted := len(model.SubmittedRequests())
			beforeMessages := len(model.State.Messages)

			invocation, ok := codextui.ParseCommand("/" + frame.Name)
			if !ok {
				t.Fatalf("ParseCommand failed for /%s", frame.Name)
			}
			_ = model.applyCommand(invocation)

			afterView := utils.StripANSI(model.View())
			changed := afterView != beforeView ||
				model.notice != beforeNotice ||
				model.modal != beforeModal ||
				len(model.SubmittedRequests()) != beforeSubmitted ||
				len(model.State.Messages) != beforeMessages
			if !changed {
				t.Fatalf("/%s produced no visible result", frame.Name)
			}
			if strings.Contains(afterView, "Unknown command") {
				t.Fatalf("/%s rendered unknown command:\n%s", frame.Name, afterView)
			}
		})
	}
}

func TestModelSlashCommandPopupFiltersCompletesAndDispatches(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})

	typeText(t, model, "/")
	if !model.slashPopup.Active {
		t.Fatal("slash popup should be active after typing /")
	}
	if len(model.slashPopup.Items) == 0 || model.slashPopup.Items[0].Name != "model" {
		t.Fatalf("first slash popup item = %#v, want model first", model.slashPopup.Items)
	}
	view := model.View()
	for _, want := range []string{codextui.SelectionPrefix(true) + "/model", "choose what model and reasoning effort to use"} {
		if !strings.Contains(view, want) {
			t.Fatalf("slash popup view missing %q:\n%s", want, view)
		}
	}

	typeText(t, model, "mo")
	if len(model.slashPopup.Items) != 1 || model.slashPopup.Items[0].Name != "model" {
		t.Fatalf("filtered slash popup items = %#v, want only model", model.slashPopup.Items)
	}
	model.Update(key(bubbletea.KeyTab))
	if got := model.ComposerValue(); got != "/model " {
		t.Fatalf("composer after Tab completion = %q, want /model space", got)
	}
	if model.slashPopup.Active {
		t.Fatal("slash popup should close after Tab completion")
	}

	model = NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	typeText(t, model, "/")
	model.Update(key(bubbletea.KeyDown))
	if got := model.selectedSlashPopupName(); got != "ide" {
		t.Fatalf("selected after Down = %q, want ide", got)
	}
	if view := model.View(); !strings.Contains(view, codextui.SelectionPrefix(true)+"/ide") {
		t.Fatalf("slash popup should render selected color row for /ide:\n%s", view)
	}

	model = NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	typeText(t, model, "/")
	model.Update(key(bubbletea.KeyEnter))
	if model.slashPopup.Active {
		t.Fatal("slash popup should close after Enter dispatch")
	}
	if got := model.ComposerValue(); got != "" {
		t.Fatalf("composer after Enter dispatch = %q, want empty", got)
	}
	if view := model.View(); !strings.Contains(view, "Select Model") {
		t.Fatalf("Enter on /model should open model picker:\n%s", view)
	}
}

func TestModelFastSlashCommandTogglesAndPropagatesServiceTier(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{FeatureSettings: map[string]bool{"fast_mode": true}, ServiceTierCommands: []bottompane.ServiceTierCommand{{ID: "priority", Name: "fast", Description: "Fastest inference"}}})

	typeText(t, model, "/fast")
	model.Update(key(bubbletea.KeyEnter))
	if state.ServiceTier != chatwidget.ServiceTierFastRequestValue || !strings.Contains(model.View(), "Service tier set to priority") {
		t.Fatalf("fast enable state=%q view=%s", state.ServiceTier, model.View())
	}
	typeText(t, model, "hello")
	model.Update(key(bubbletea.KeyEnter))
	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].ServiceTier != chatwidget.ServiceTierFastRequestValue {
		t.Fatalf("submitted requests = %#v", requests)
	}
}

func TestModelFastSlashCommandUsesCatalogTierAndHidesWithoutSupport(t *testing.T) {
	unsupported := NewModel(codextui.NewState(nil), Options{FeatureSettings: map[string]bool{"fast_mode": true}})
	for _, item := range unsupported.slashPopupCatalog() {
		if item.Name == "fast" {
			t.Fatal("fast command visible without a catalog tier")
		}
	}

	state := codextui.NewState(nil)
	var writes [][]SettingsEdit
	model := NewModel(state, Options{
		FeatureSettings:     map[string]bool{"fast_mode": true},
		ServiceTierCommands: []bottompane.ServiceTierCommand{{ID: "turbo-id", Name: "fast", Description: "Catalog description"}},
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			return SettingsWriteResult{FilePath: "config.toml"}, nil
		},
	})
	typeText(t, model, "/fast")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if state.ServiceTier != "turbo-id" || cmd == nil {
		t.Fatalf("state tier=%q cmd=%v", state.ServiceTier, cmd)
	}
	if msg := cmd(); msg != nil {
		model.Update(msg)
	}
	if len(writes) != 1 || len(writes[0]) != 1 || writes[0][0].KeyPath != "service_tier" || writes[0][0].Value != "fast" {
		t.Fatalf("writes = %#v", writes)
	}
	if !strings.Contains(model.View(), "Service tier set to turbo-id") {
		t.Fatalf("view = %s", model.View())
	}
}

func TestModelModalSelectionRendersColorBar(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 90, Height: 20})
	model.Update(ApprovalRequestMsg{
		ID:      "approval-color",
		Title:   "Run command?",
		Command: "go test ./...",
	})

	view := model.View()
	for _, want := range []string{"\x1b[", codextui.NumberedSelectionPrefix(0, true) + "Allow for this turn"} {
		if !strings.Contains(view, want) {
			t.Fatalf("modal selected row missing %q:\n%s", want, view)
		}
	}
	model.Update(key(bubbletea.KeyDown))
	if view := model.View(); !strings.Contains(view, codextui.NumberedSelectionPrefix(1, true)+"Allow for this session") {
		t.Fatalf("modal selected row did not move color bar:\n%s", view)
	}
}

func TestModelPermissionsMenuAppliesRuntimeState(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		ApprovalPolicy: "on-request",
		Sandbox:        chatwidget.WorkspaceProfile,
	})
	model := NewModel(state, Options{Width: 90, Height: 24})

	typeText(t, model, "/permissions")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"Update Model Permissions", "Read Only", "Ask for approval", "Full Access"} {
		if !strings.Contains(view, want) {
			t.Fatalf("permissions menu missing %q:\n%s", want, view)
		}
	}
	model.Update(key(bubbletea.KeyEnter))
	if state.ApprovalPolicy != string(chatwidget.ApprovalOnRequest) || state.Sandbox != chatwidget.ReadOnlyProfile {
		t.Fatalf("read-only state approval=%q sandbox=%q", state.ApprovalPolicy, state.Sandbox)
	}

	typeText(t, model, "/permissions")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Full Access") || !strings.Contains(view, "Yes, continue anyway") {
		t.Fatalf("full access confirmation missing:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEnter))
	if state.ApprovalPolicy != string(chatwidget.ApprovalNever) || state.Sandbox != chatwidget.DangerFullAccessProfile {
		t.Fatalf("full-access state approval=%q sandbox=%q", state.ApprovalPolicy, state.Sandbox)
	}
	if !strings.Contains(model.View(), "Permissions: Full Access") {
		t.Fatalf("permissions notice missing:\n%s", model.View())
	}
}

func TestModelPermissionsMenuHonorsRequirements(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		ApprovalPolicy: "on-request",
		Sandbox:        chatwidget.WorkspaceProfile,
	})
	model := NewModel(state, Options{
		Width:  90,
		Height: 24,
		PermissionRequirements: &chatwidget.PermissionRequirements{
			AllowedApprovalPolicies: []chatwidget.ApprovalPolicy{chatwidget.ApprovalOnRequest},
			AllowedProfiles:         map[string]bool{chatwidget.WorkspaceProfile: true, chatwidget.ReadOnlyProfile: true},
		},
	})

	typeText(t, model, "/permissions")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	if !strings.Contains(view, "Full Access") || !strings.Contains(view, "Disabled by requirements.") {
		t.Fatalf("permissions menu should show disabled full access:\n%s", view)
	}
	if model.modal == nil {
		t.Fatal("permissions modal is nil")
	}
	fullAccessIndex := -1
	for index, option := range model.modal.options {
		if option.Label == "Full Access" {
			fullAccessIndex = index
			if !option.Disabled {
				t.Fatalf("full access option should be disabled: %#v", option)
			}
		}
	}
	if fullAccessIndex < 0 {
		t.Fatalf("full access option not found: %#v", model.modal.options)
	}
	model.modal.selected = fullAccessIndex
	model.Update(key(bubbletea.KeyEnter))

	if state.ApprovalPolicy != string(chatwidget.ApprovalOnRequest) || state.Sandbox != chatwidget.WorkspaceProfile {
		t.Fatalf("disabled full-access changed state approval=%q sandbox=%q", state.ApprovalPolicy, state.Sandbox)
	}
	if view := model.View(); strings.Contains(view, "Yes, continue anyway") {
		t.Fatalf("disabled full access opened confirmation:\n%s", view)
	}
}

func TestModelWindowsSandboxSetupUsesCallbackAndCompletion(t *testing.T) {
	state := codextui.NewState(nil)
	var calls []chatwidget.WindowsSandboxMode
	model := NewModel(state, Options{
		Width:            90,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnStartWindowsSandboxSetup: func(mode chatwidget.WindowsSandboxMode, cwd string) (WindowsSandboxSetupOutcome, error) {
			calls = append(calls, mode)
			if cwd != `D:\repo` {
				t.Fatalf("cwd = %q, want D:\\repo", cwd)
			}
			return WindowsSandboxSetupOutcome{
				Started: true,
				Completion: &WindowsSandboxSetupCompletion{
					Mode:    mode,
					Success: true,
				},
			}, nil
		},
	})

	typeText(t, model, "/setup-default-sandbox")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(model.View(), "Setting up sandbox...") || !strings.Contains(model.View(), "Input disabled until setup completes.") {
		t.Fatalf("setup status missing:\n%s", model.View())
	}
	runTeaCmd(t, model, cmd)

	if len(calls) != 1 || calls[0] != chatwidget.WindowsSandboxModeElevated {
		t.Fatalf("setup calls = %#v", calls)
	}
	if !strings.Contains(model.View(), "Windows sandbox setup completed.") {
		t.Fatalf("completion notice missing:\n%s", model.View())
	}
}

func TestModelWindowsSandboxSetupCompletionNotificationClearsStatus(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:            90,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnStartWindowsSandboxSetup: func(mode chatwidget.WindowsSandboxMode, cwd string) (WindowsSandboxSetupOutcome, error) {
			return WindowsSandboxSetupOutcome{Started: true}, nil
		},
	})

	typeText(t, model, "/setup-default-sandbox")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if !strings.Contains(model.View(), "Setting up sandbox...") || !model.windowsSandboxSetupActive {
		t.Fatalf("setup status missing before completion:\n%s", model.View())
	}

	model.Update(WindowsSandboxSetupCompletedMsg{Completion: WindowsSandboxSetupCompletion{
		Mode:    chatwidget.WindowsSandboxModeElevated,
		Success: true,
	}})
	if model.windowsSandboxSetupActive || strings.Contains(model.View(), "Input disabled until setup completes.") || !strings.Contains(model.View(), "Windows sandbox setup completed.") {
		t.Fatalf("completion did not clear setup status:\n%s", model.View())
	}
}

func TestModelWindowsSandboxSetupHonorsRequirements(t *testing.T) {
	calls := 0
	model := NewModel(codextui.NewState(nil), Options{
		PermissionRequirements: &chatwidget.PermissionRequirements{
			AllowedWindowsSandboxModes: []chatwidget.WindowsSandboxMode{chatwidget.WindowsSandboxModeUnelevated},
		},
		OnStartWindowsSandboxSetup: func(mode chatwidget.WindowsSandboxMode, cwd string) (WindowsSandboxSetupOutcome, error) {
			calls++
			return WindowsSandboxSetupOutcome{Started: true}, nil
		},
	})

	typeText(t, model, "/setup-default-sandbox")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd != nil {
		t.Fatalf("disallowed setup returned cmd %#v", cmd)
	}
	if calls != 0 {
		t.Fatalf("setup callback called %d times", calls)
	}
	if !strings.Contains(model.View(), windowsSandboxDisallowedNotice) {
		t.Fatalf("disallowed notice missing:\n%s", model.View())
	}
}

func TestModelRustSlashSettingsDebugAndMCPCommands(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{
		Width:           90,
		Height:          24,
		FeatureSettings: map[string]bool{"memories": true},
		MCPServers: []historycell.McpServerStatus{{
			Name: "docs",
			Auth: "OAuth",
			Tools: []string{
				"read",
				"search",
			},
			Resources: []historycell.McpResource{{Name: "guide", URI: "file://guide"}},
		}},
	})

	typeText(t, model, "/personality")
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Select Personality") || !strings.Contains(view, "Pragmatic") {
		t.Fatalf("personality modal missing:\n%s", view)
	}
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	if state.Personality != string(chatwidget.PersonalityPragmatic) {
		t.Fatalf("Personality = %q, want pragmatic", state.Personality)
	}

	typeText(t, model, "/experimental")
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Experimental Features") || !strings.Contains(view, "Memories") {
		t.Fatalf("experimental modal missing:\n%s", view)
	}
	model.Update(key(bubbletea.KeySpace))
	model.Update(key(bubbletea.KeyEnter))
	if model.featureSettings["memories"] {
		t.Fatalf("memories feature should have toggled off: %#v", model.featureSettings)
	}

	typeText(t, model, "/debug-config")
	model.Update(key(bubbletea.KeyEnter))
	for _, want := range []string{"Config layer stack", "Requirements:", "Session:", "model: gpt-5"} {
		if !strings.Contains(model.View(), want) {
			t.Fatalf("debug config output missing %q:\n%s", want, model.View())
		}
	}

	typeText(t, model, "/mcp verbose")
	model.Update(key(bubbletea.KeyEnter))
	for _, want := range []string{"MCP Tools", "docs", "Tools: read, search", "Resources: guide (file://guide)"} {
		if !strings.Contains(model.View(), want) {
			t.Fatalf("mcp verbose output missing %q:\n%s", want, model.View())
		}
	}
}

func TestModelSettingsCommandsPersistSelections(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	var writes [][]SettingsEdit
	model := NewModel(state, Options{
		Width:           90,
		Height:          24,
		FeatureSettings: map[string]bool{"memories": true},
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			result := SettingsWriteResult{
				FeatureSettings: map[string]bool{"memories": false},
				Personality:     chatwidget.PersonalityPragmatic,
				FilePath:        `D:\codex\config.toml`,
			}
			return result, nil
		},
	})

	typeText(t, model, "/personality pragmatic")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(writes) != 1 || len(writes[0]) != 1 || writes[0][0].KeyPath != "personality" || writes[0][0].Value != "pragmatic" {
		t.Fatalf("personality writes = %#v", writes)
	}
	if state.Personality != string(chatwidget.PersonalityPragmatic) || !strings.Contains(model.View(), "Saved to") {
		t.Fatalf("personality state/view mismatch: state=%q view=\n%s", state.Personality, model.View())
	}

	typeText(t, model, "/experimental memories off")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(writes) != 2 || len(writes[1]) != 1 || writes[1][0].KeyPath != "features.memories" || writes[1][0].Value != false {
		t.Fatalf("experimental writes = %#v", writes)
	}
	if model.featureSettings["memories"] {
		t.Fatalf("memories feature should be false after save: %#v", model.featureSettings)
	}
}

func TestModelThemeAndPetsCommandsPersistSelections(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	var writes [][]SettingsEdit
	model := NewModel(state, Options{
		Width:  90,
		Height: 24,
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			result := SettingsWriteResult{FilePath: `D:\codex\config.toml`}
			for _, edit := range edits {
				switch edit.KeyPath {
				case "tui.theme":
					result.TUITheme, _ = edit.Value.(string)
				case "tui.pet":
					result.TUIPet, _ = edit.Value.(string)
				}
			}
			return result, nil
		},
	})

	typeText(t, model, "/theme dracula")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(writes) != 1 || len(writes[0]) != 1 || writes[0][0].KeyPath != "tui.theme" || writes[0][0].Value != "dracula" {
		t.Fatalf("theme writes = %#v", writes)
	}
	if model.tuiTheme != "dracula" || !strings.Contains(model.View(), "Theme set to Dracula. Saved to") {
		t.Fatalf("theme state/view mismatch: theme=%q view=\n%s", model.tuiTheme, model.View())
	}

	typeText(t, model, "/pets off")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(writes) != 2 || len(writes[1]) != 1 || writes[1][0].KeyPath != "tui.pet" || writes[1][0].Value != chatwidget.DisabledPetID {
		t.Fatalf("pet writes = %#v", writes)
	}
	if model.tuiPet != chatwidget.DisabledPetID || !strings.Contains(model.View(), "Terminal pets disabled. Saved to") {
		t.Fatalf("pet state/view mismatch: pet=%q view=\n%s", model.tuiPet, model.View())
	}
}

func TestModelThemeAndPetsPickersUseCurrentSelections(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{
		Width:    90,
		Height:   24,
		TUITheme: "dracula",
		TUIPet:   "dewey",
	})

	typeText(t, model, "/theme")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{
		"Select Syntax Theme",
		"Type to filter themes...",
		"dracula (current)",
		"summarize",
		"Press enter to confirm or esc to go back",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("theme picker missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "1. 1337") || strings.Contains(view, "Dracula - Built in") {
		t.Fatalf("theme picker missing Rust-like content:\n%s", view)
	}

	model.Update(key(bubbletea.KeyEsc))
	typeText(t, model, "/pets")
	model.Update(key(bubbletea.KeyEnter))
	view = model.View()
	for _, want := range []string{"Select Pet", "Disable terminal pets", "Dewey (current)", "Null Signal"} {
		if !strings.Contains(view, want) {
			t.Fatalf("pets picker missing %q:\n%s", want, view)
		}
	}
}

func TestModelThemePickerFiltersAndPersistsSelection(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	var writes [][]SettingsEdit
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			return SettingsWriteResult{
				FilePath: `D:\codex\config.toml`,
				TUITheme: "dracula",
			}, nil
		},
	})

	typeText(t, model, "/theme")
	model.Update(key(bubbletea.KeyEnter))
	typeText(t, model, "dracula")
	view := model.View()
	if !strings.Contains(view, "Filter: dracula") || !strings.Contains(view, "dracula") || strings.Contains(view, "base16-ocean-dark") {
		t.Fatalf("theme filter view mismatch:\n%s", view)
	}

	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.tuiTheme != "dracula" {
		t.Fatalf("theme = %q, want dracula", model.tuiTheme)
	}
	if len(writes) != 1 || len(writes[0]) != 1 || writes[0][0].KeyPath != "tui.theme" || writes[0][0].Value != "dracula" {
		t.Fatalf("writes = %#v", writes)
	}
	if !strings.Contains(model.View(), "Theme set to Dracula. Saved to") {
		t.Fatalf("theme save notice missing:\n%s", model.View())
	}
}

func TestModelThemePickerPreviewPaletteTracksSelection(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{
		Width:    100,
		Height:   24,
		TUITheme: "dracula",
	})

	typeText(t, model, "/theme")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.themePicker == nil {
		t.Fatal("theme picker did not open")
	}
	beforeID := model.modal.themePicker.PreviewThemeID()
	beforePreview := strings.Join(renderThemePreviewRows(codextui.ThemePreviewRows(48), 48, beforeID), "\n")
	model.Update(key(bubbletea.KeyDown))
	afterID := model.modal.themePicker.PreviewThemeID()
	afterPreview := strings.Join(renderThemePreviewRows(codextui.ThemePreviewRows(48), 48, afterID), "\n")

	if beforeID == afterID {
		t.Fatalf("preview theme did not move: %q", beforeID)
	}
	if beforePreview == afterPreview {
		t.Fatalf("preview rendering did not change when selection moved from %q to %q", beforeID, afterID)
	}
	if !strings.Contains(beforePreview, "\x1b[") || !strings.Contains(afterPreview, "\x1b[") {
		t.Fatalf("preview should be ANSI styled before=%q after=%q", beforePreview, afterPreview)
	}
}

func TestModelThemeAppliesToAssistantMarkdownCodeBlocks(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	state.AddMessage(codextui.RoleAssistant, "```go\npackage main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n```")
	model := NewModel(state, Options{
		Width:    100,
		Height:   24,
		TUITheme: "dracula",
	})

	dracula := model.View()
	if !strings.Contains(dracula, "\x1b[") || !strings.Contains(dracula, "package") {
		t.Fatalf("assistant code block was not highlighted:\n%s", dracula)
	}
	model.setTUITheme("github")
	github := model.View()
	if dracula == github {
		t.Fatalf("assistant code block did not re-render after theme change")
	}
}

func TestAssistantMarkdownCodeBlockPreservesSourceLines(t *testing.T) {
	source := "下面是 C 代码：\n\n```c\n#include <stdio.h>\n\nvoid swap(int *a, int *b) {\n    int temp = *a;\n    *a = *b;\n    *b = temp;\n}\n```"
	lines := richMessageDisplayLines(codextui.Message{Role: codextui.RoleAssistant, Text: source}, 24, "dracula")
	cleanLines := make([]string, 0, len(lines))
	for _, line := range lines {
		cleanLines = append(cleanLines, strings.TrimSpace(utils.StripANSI(line)))
	}

	for _, want := range []string{
		"#include <stdio.h>",
		"void swap(int *a, int *b) {",
		"int temp = *a;",
		"*a = *b;",
		"*b = temp;",
		"}",
	} {
		found := false
		for _, line := range cleanLines {
			if line == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("rendered code block lost source line %q:\n%s", want, strings.Join(cleanLines, "\n"))
		}
	}
	for _, fragment := range []string{"#include", "void", "swap(int", "int temp", "= *a;"} {
		for _, line := range cleanLines {
			if line == fragment {
				t.Fatalf("code line was split at ANSI token boundary %q:\n%s", fragment, strings.Join(cleanLines, "\n"))
			}
		}
	}
}

func TestModelThemeAppliesToExecCommandCells(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{
		Width:    100,
		Height:   24,
		TUITheme: "dracula",
	})

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.ToolCallItemWithCallID(
		"fc-1",
		"call-1",
		"exec_command",
		`{"cmd":"Get-ChildItem -LiteralPath 'tmp\\pdfs' -Filter 'test-*.png'"}`,
	))})

	view := model.View()
	if !strings.Contains(view, "\x1b[") {
		t.Fatalf("themed exec command should include ANSI styling:\n%s", view)
	}
	clean := utils.StripANSI(view)
	if !strings.Contains(clean, "Running Get-ChildItem -LiteralPath 'tmp\\pdfs' -Filter 'test-*.png'") {
		t.Fatalf("stripped themed exec command lost content:\n%s", clean)
	}
	if strings.Contains(clean, "Running exec_command") {
		t.Fatalf("exec command tool name leaked into themed cell:\n%s", clean)
	}
	if len(state.Messages) == 0 || strings.Contains(state.Messages[0].RawText, "\x1b[") {
		t.Fatalf("raw exec transcript should remain unstyled: %#v", state.Messages)
	}
}

func TestModelRustSlashLongTailCommandSurfaces(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{Width: 90, Height: 24})

	typeText(t, model, "/review")
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Select a review preset") || !strings.Contains(view, "Review uncommitted changes") {
		t.Fatalf("review modal missing:\n%s", view)
	}
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Review start requires app-server review/start.") || !strings.Contains(view, "target: uncommitted changes") {
		t.Fatalf("review selection missing history:\n%s", model.View())
	}

	typeText(t, model, "/skills")
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Skills") || !strings.Contains(view, "List skills") {
		t.Fatalf("skills modal missing:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEnter))
	if model.ComposerValue() != "$" {
		t.Fatalf("skills list should insert $, got %q", model.ComposerValue())
	}
	model.composer.Reset()

	typeText(t, model, "/mention")
	model.Update(key(bubbletea.KeyEnter))
	if model.ComposerValue() != "@" {
		t.Fatalf("mention should insert @, got %q", model.ComposerValue())
	}
	model.composer.Reset()

	typeText(t, model, "/vim")
	model.Update(key(bubbletea.KeyEnter))
	if !model.vimMode || !strings.Contains(model.View(), "Vim mode enabled.") {
		t.Fatalf("vim toggle failed:\n%s", model.View())
	}

	typeText(t, model, "/plan investigate architecture")
	model.Update(key(bubbletea.KeyEnter))
	if !state.PlanMode {
		t.Fatalf("PlanMode = false, want true")
	}
	if got := model.SubmittedRequests(); len(got) != 1 || got[0].Prompt != "investigate architecture" {
		t.Fatalf("plan prompt requests = %#v", got)
	}
}

func TestModelRuntimeBackedSlashCommands(t *testing.T) {
	state := codextui.NewState(&codextui.Options{CWD: `D:\repo`})
	state.SetThreadID("thread-runtime")
	opened := ""
	granted := ""
	imported := ""
	model := NewModel(state, Options{
		OnOpenDesktopThread: func(threadID string) error { opened = threadID; return nil },
		OnReadRolloutPath:   func(threadID string) (string, error) { return `D:\rollouts\thread-runtime.jsonl`, nil },
		OnSandboxReadDir:    func(path string) error { granted = path; return nil },
		OnImportExternalAgent: func(cwd string) (string, error) {
			imported = cwd
			return "External agent import started: import-1", nil
		},
		FeatureSettings: map[string]bool{"memories": true, "memory_generation": false},
	})

	for _, command := range []string{"/app", "/rollout", `/sandbox-add-read-dir D:\data`, "/import", "/memories"} {
		invocation, ok := codextui.ParseCommand(command)
		if !ok {
			t.Fatalf("ParseCommand(%q) failed", command)
		}
		_ = model.applyCommand(invocation)
	}
	if opened != "thread-runtime" {
		t.Fatalf("opened thread = %q", opened)
	}
	if granted != `D:\data` {
		t.Fatalf("granted path = %q", granted)
	}
	if imported != `D:\repo` {
		t.Fatalf("import cwd = %q", imported)
	}
	if model.modal == nil || model.modal.kind != ModalKindMemories {
		t.Fatalf("memories modal = %#v", model.modal)
	}
	if !strings.Contains(model.View(), "Memories") {
		t.Fatalf("view missing memories popup:\n%s", model.View())
	}
}

func TestModelStatusLineAndTitleCommands(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:           "gpt-5",
		ReasoningEffort: "high",
		ApprovalPolicy:  "on-request",
		Sandbox:         "workspace-write",
	})
	state.SetStatus("idle")
	writes := []string{}
	model := NewModel(state, Options{
		Width:            90,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnWriteTerminalTitle: func(sequence string) bubbletea.Cmd {
			writes = append(writes, sequence)
			return nil
		},
	})

	typeText(t, model, "/statusline model-with-reasoning current-dir run-state")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"gpt-5", "high", "repo", "idle"} {
		if !strings.Contains(view, want) {
			t.Fatalf("statusline view missing %q:\n%s", want, view)
		}
	}

	typeText(t, model, "/title app-name project-name run-state")
	model.Update(key(bubbletea.KeyEnter))
	if len(writes) != 1 {
		t.Fatalf("terminal title writes = %#v, want one write", writes)
	}
	for _, want := range []string{"\x1b]0;", "codex", "repo", "idle"} {
		if !strings.Contains(writes[0], want) {
			t.Fatalf("terminal title OSC missing %q: %q", want, writes[0])
		}
	}

	typeText(t, model, "/title off")
	model.Update(key(bubbletea.KeyEnter))
	if len(writes) != 2 || writes[1] != codextui.ClearTerminalTitleOSC() {
		t.Fatalf("terminal title clear writes = %#v", writes)
	}
}

func TestModelStatusControlsSetupHistoryAndInvalidItem(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{Width: 90, Height: 24, SessionPickerCWD: `D:\repo`})

	typeText(t, model, "/statusline")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"Status line setup", "Select which items", "[x] model-with-reasoning", "Space toggle"} {
		if !strings.Contains(view, want) {
			t.Fatalf("statusline setup modal missing %q:\n%s", want, view)
		}
	}
	model.Update(key(bubbletea.KeyEsc))

	typeText(t, model, "/title does-not-exist")
	model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(model.View(), "Unknown terminal title item: does-not-exist") {
		t.Fatalf("invalid title item notice missing:\n%s", model.View())
	}
}

func TestModelStatusLineCommandConfiguresHeader(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:           "gpt-5",
		ReasoningEffort: "high",
		Sandbox:         "workspace-write",
	})
	model := NewModel(state, Options{Width: 100, Height: 24})

	typeText(t, model, "/statusline model permissions approval-mode")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"gpt-5", "workspace-write"} {
		if !strings.Contains(view, want) {
			t.Fatalf("status line view missing %q:\n%s", want, view)
		}
	}
}

func TestModelStatusLineSetupModalTogglesAndSaves(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	model := NewModel(state, Options{Width: 100, Height: 24})

	typeText(t, model, "/statusline")
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Status line setup") || !strings.Contains(view, "Space toggle | Enter save | Esc cancel") {
		t.Fatalf("status line setup modal missing:\n%s", view)
	}

	model.Update(key(bubbletea.KeySpace))
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "gpt-5") {
		t.Fatalf("saved status line missing selected model:\n%s", view)
	}
}

func TestModelTerminalTitleCommandWritesOSC(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	var writes []string
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnWriteTerminalTitle: func(sequence string) bubbletea.Cmd {
			writes = append(writes, sequence)
			return nil
		},
	})

	typeText(t, model, "/title app-name model")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd != nil {
		cmd()
	}
	if len(writes) != 1 || !strings.Contains(writes[0], "\x1b]0;codex | gpt-5\x07") {
		t.Fatalf("terminal title writes = %#v", writes)
	}

	typeText(t, model, "/title off")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd != nil {
		cmd()
	}
	if len(writes) != 2 || writes[1] != codextui.ClearTerminalTitleOSC() {
		t.Fatalf("terminal title clear writes = %#v", writes)
	}
}

func TestModelGoalCommandsCallRuntimeCallbacks(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	var setCalls []appserver.GoalSetParams
	var clearCalls []string
	model := NewModel(state, Options{
		Width:           100,
		Height:          24,
		StatusLineItems: []string{"task-progress"},
		OnSetGoal: func(threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error) {
			setCalls = append(setCalls, appserver.GoalSetParams{
				ThreadID:    threadID,
				Objective:   cloneStringPtrTea(objective),
				TokenBudget: cloneInt64PtrTea(tokenBudget),
				Status:      cloneGoalStatusPtr(status),
			})
			goalStatus := appserver.GoalActive
			if status != nil {
				goalStatus = *status
			}
			goalObjective := "finish parity"
			if objective != nil {
				goalObjective = *objective
			}
			return appserver.Goal{
				ThreadID:        threadID,
				Objective:       goalObjective,
				TokenBudget:     cloneInt64PtrTea(tokenBudget),
				TokensUsed:      1234,
				TimeUsedSeconds: 90,
				Status:          goalStatus,
			}, nil
		},
		OnClearGoal: func(threadID string) (bool, error) {
			clearCalls = append(clearCalls, threadID)
			return true, nil
		},
	})

	typeText(t, model, "/goal set finish parity --budget 50000")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/goal set did not return runtime command")
	}
	model.Update(cmd())
	if len(setCalls) != 1 || setCalls[0].ThreadID != "thread-goal" || setCalls[0].Objective == nil || *setCalls[0].Objective != "finish parity" || setCalls[0].TokenBudget == nil || *setCalls[0].TokenBudget != 50000 || setCalls[0].Status == nil || *setCalls[0].Status != appserver.GoalActive {
		t.Fatalf("set goal calls = %#v", setCalls)
	}
	if model.currentGoal == nil || model.currentGoal.Objective != "finish parity" {
		t.Fatalf("current goal = %#v", model.currentGoal)
	}
	if raw := state.Messages[len(state.Messages)-1].RawText; !strings.Contains(raw, "Objective: finish parity") || !strings.Contains(raw, "Token budget: 50K") {
		t.Fatalf("goal history missing summary:\n%s", raw)
	}
	if !strings.Contains(model.View(), "Goal active") {
		t.Fatalf("status line missing goal progress:\n%s", model.View())
	}

	typeText(t, model, "/goal pause")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/goal pause did not return runtime command")
	}
	model.Update(cmd())
	if len(setCalls) != 2 || setCalls[1].Status == nil || *setCalls[1].Status != appserver.GoalPaused {
		t.Fatalf("pause calls = %#v", setCalls)
	}
	if raw := state.Messages[len(state.Messages)-1].RawText; !strings.Contains(raw, "Paused goal") || !strings.Contains(raw, "Status: paused") {
		t.Fatalf("paused goal history missing:\n%s", raw)
	}

	typeText(t, model, "/goal clear")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/goal clear did not return runtime command")
	}
	model.Update(cmd())
	if len(clearCalls) != 1 || clearCalls[0] != "thread-goal" {
		t.Fatalf("clear calls = %#v", clearCalls)
	}
	if model.currentGoal != nil {
		t.Fatalf("current goal after clear = %#v, want nil", model.currentGoal)
	}
	if raw := state.Messages[len(state.Messages)-1].RawText; !strings.Contains(raw, "No goal set.") {
		t.Fatalf("clear goal history missing empty state:\n%s", raw)
	}
}

func TestModelGoalReadAndNotifications(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	model := NewModel(state, Options{
		Width:           100,
		Height:          24,
		StatusLineItems: []string{"task-progress"},
		OnReadGoal: func(threadID string) (*appserver.Goal, error) {
			if threadID != "thread-goal" {
				t.Fatalf("read threadID = %q, want thread-goal", threadID)
			}
			return nil, nil
		},
	})
	model.currentGoal = &appserver.Goal{ThreadID: "thread-goal", Objective: "old", Status: appserver.GoalActive}

	typeText(t, model, "/goal")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/goal did not return read command")
	}
	model.Update(cmd())
	if model.currentGoal != nil {
		t.Fatalf("current goal after nil read = %#v, want nil", model.currentGoal)
	}
	if raw := state.Messages[len(state.Messages)-1].RawText; !strings.Contains(raw, "No goal set.") {
		t.Fatalf("goal read empty history missing:\n%s", raw)
	}

	model.Update(GoalUpdatedMsg{Goal: appserver.Goal{
		ThreadID:        "thread-goal",
		Objective:       "ship runtime",
		TokensUsed:      1200,
		TimeUsedSeconds: 60,
		Status:          appserver.GoalActive,
	}})
	if model.currentGoal == nil || model.currentGoal.Objective != "ship runtime" {
		t.Fatalf("current goal after notification = %#v", model.currentGoal)
	}
	if !strings.Contains(model.View(), "Goal active") {
		t.Fatalf("goal notification did not refresh status surface:\n%s", model.View())
	}
	model.Update(GoalClearedMsg{ThreadID: "thread-goal"})
	if model.currentGoal != nil {
		t.Fatalf("current goal after clear notification = %#v", model.currentGoal)
	}
}

func TestModelUsageCommandOpensMenuAndShowsTokenActivity(t *testing.T) {
	two := int64(2)
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width:                          80,
		Height:                         24,
		HasChatGPTAccount:              true,
		AvailableRateLimitResetCredits: &two,
	})

	typeText(t, model, "/usage")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"Usage", "View account usage", "Show usage", "Redeem usage limit reset", "You have 2 usage limit resets available."} {
		if !strings.Contains(view, want) {
			t.Fatalf("usage menu missing %q:\n%s", want, view)
		}
	}

	model.Update(key(bubbletea.KeyEnter))
	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("history count = %d, want 1; messages=%#v", got, state.Messages)
	}
	raw := state.Messages[0].RawText
	for _, want := range []string{"/usage daily", "Token activity", "Token activity unavailable"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("usage history missing %q:\n%s", want, raw)
		}
	}
}

func TestModelUsageCommandDirectViewAndInvalidView(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	typeText(t, model, "/usage year")
	model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(model.View(), "Usage: /usage [daily|weekly|cumulative]") {
		t.Fatalf("invalid usage view missing notice:\n%s", model.View())
	}

	typeText(t, model, "/usage weekly")
	model.Update(key(bubbletea.KeyEnter))
	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("history count = %d, want 1; messages=%#v", got, state.Messages)
	}
	if raw := state.Messages[0].RawText; !strings.Contains(raw, "/usage weekly") || !strings.Contains(raw, "Token activity unavailable") {
		t.Fatalf("usage history = %q", raw)
	}
}

func TestModelUsageCommandReadsTokenActivity(t *testing.T) {
	state := codextui.NewState(nil)
	lifetime := int64(12345)
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnReadTokenActivity: func(view chatwidget.TokenActivityView) (chatwidget.TokenActivityResponse, error) {
			if view != chatwidget.TokenActivityWeekly {
				t.Fatalf("read token activity view = %q, want weekly", view)
			}
			return chatwidget.TokenActivityResponse{
				Summary: chatwidget.TokenActivitySummary{LifetimeTokens: &lifetime},
				DailyUsageBuckets: &[]chatwidget.TokenActivityDailyBucket{
					{StartDate: fixedTeaTime().Format("2006-01-02"), Tokens: 9},
				},
			}, nil
		},
	})

	typeText(t, model, "/usage weekly")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("usage command did not return token activity cmd")
	}
	model.Update(cmd())
	if got := countRole(state.Messages, codextui.RoleHistory); got != 2 {
		t.Fatalf("history count = %d, want loading and loaded cards; messages=%#v", got, state.Messages)
	}
	loaded := state.Messages[1].RawText
	for _, want := range []string{"Token activity", "Lifetime 12.3K", "Each column = 1 week", "WEEKLY"} {
		if !strings.Contains(loaded, want) {
			t.Fatalf("loaded usage history missing %q:\n%s", want, loaded)
		}
	}
}

func TestModelUsageMenuResetStates(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	typeText(t, model, "/usage")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	if !strings.Contains(view, "Redeem usage limit reset [disabled]") || !strings.Contains(view, "No usage limit resets available.") {
		t.Fatalf("usage menu disabled reset missing:\n%s", view)
	}
	model.Update(key(bubbletea.KeyDown))
	if !strings.Contains(model.View(), codextui.NumberedSelectionPrefix(0, true)+"Show usage") {
		t.Fatalf("disabled reset should be skipped:\n%s", model.View())
	}

	two := int64(2)
	model = NewModel(state, Options{
		Width:                          80,
		Height:                         24,
		HasChatGPTAccount:              true,
		AvailableRateLimitResetCredits: &two,
	})
	typeText(t, model, "/usage")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	view = model.View()
	for _, want := range []string{"Usage limit resets", "You have 2 usage limit resets available.", "Use a reset", "Reset your current 5-hour and weekly usage limits."} {
		if !strings.Contains(view, want) {
			t.Fatalf("reset confirmation missing %q:\n%s", want, view)
		}
	}

	model = NewModel(state, Options{
		Width:                          80,
		Height:                         24,
		HasChatGPTAccount:              true,
		ChatGPTPlanType:                "free",
		AvailableRateLimitResetCredits: &two,
	})
	typeText(t, model, "/usage")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	if view = model.View(); !strings.Contains(view, "Reset your current monthly usage limit.") {
		t.Fatalf("free plan reset confirmation should use monthly copy:\n%s", view)
	}
}

func TestModelUsageRateLimitResetCallbacks(t *testing.T) {
	state := codextui.NewState(nil)
	creditReads := []int64{2, 1}
	consumedKey := ""
	model := NewModel(state, Options{
		Width:             80,
		Height:            24,
		HasChatGPTAccount: true,
		OnReadRateLimitResetCredits: func() (int64, error) {
			if len(creditReads) == 0 {
				t.Fatal("unexpected extra credit read")
			}
			value := creditReads[0]
			creditReads = creditReads[1:]
			return value, nil
		},
		OnConsumeRateLimitResetCredit: func(idempotencyKey string) (chatwidget.RateLimitResetConsumeOutcome, error) {
			consumedKey = idempotencyKey
			return chatwidget.RateLimitResetOutcomeReset, nil
		},
	})

	typeText(t, model, "/usage")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyDown))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("opening reset did not request credits")
	}
	if !strings.Contains(model.View(), "Checking your available resets") {
		t.Fatalf("loading reset view missing:\n%s", model.View())
	}
	model.Update(cmd())
	if !strings.Contains(model.View(), "You have 2 usage limit resets available.") {
		t.Fatalf("credits result did not open confirmation:\n%s", model.View())
	}

	model.Update(key(bubbletea.KeyUp))
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("consume reset did not return command")
	}
	if !strings.Contains(model.View(), "Resetting your usage") {
		t.Fatalf("consume loading view missing:\n%s", model.View())
	}
	_, cmd = model.Update(cmd())
	if consumedKey == "" {
		t.Fatal("consume callback received empty idempotency key")
	}
	if cmd == nil {
		t.Fatal("successful reset did not request refreshed credits")
	}
	model.Update(cmd())
	if !strings.Contains(model.View(), "Usage reset. You have 1 usage limit reset left.") {
		t.Fatalf("post-reset credits refresh missing:\n%s", model.View())
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
				Preview:   "Resume Me",
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
		OnResumeSession: func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
			if selection.Target.ThreadID != "thread-resume" {
				t.Fatalf("resume target = %#v", selection.Target)
			}
			return SessionResumeResponse{
				Messages: []codextui.Message{
					{Role: codextui.RoleUser, Text: "restored prompt"},
					{Role: codextui.RoleAssistant, Text: "restored answer"},
				},
				Status: "idle",
			}, nil
		},
	})
	model.now = func() time.Time { return now.Add(48 * time.Hour) }

	typeText(t, model, "/resume")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{
		"Resume a previous session",
		"Type to search",
		"Filter: [Cwd] All",
		"Sort: [Updated] Created",
		"Resume Me",
		"2d ago",
		"enter resume",
		"esc exit",
		"ctrl+o comfy",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("resume picker missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "branch: main") || strings.Contains(view, "provider:") {
		t.Fatalf("dense resume picker should not show comfortable metadata:\n%s", view)
	}
	if strings.Contains(view, "Other Workspace") {
		t.Fatalf("resume picker should respect cwd filter:\n%s", view)
	}

	model.Update(key(bubbletea.KeyEnter))
	if state.ThreadID != "thread-resume" {
		t.Fatalf("ThreadID = %q, want thread-resume", state.ThreadID)
	}
	if len(state.Messages) != 2 || state.Messages[0].Text != "restored prompt" || state.Messages[1].Text != "restored answer" {
		t.Fatalf("restored messages = %#v", state.Messages)
	}
	if view := model.View(); !strings.Contains(view, "restored prompt") || !strings.Contains(view, "restored answer") {
		t.Fatalf("restored history not rendered:\n%s", view)
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != string(codextui.SessionSelectionResume) || responses[0].Picker.Value != "thread-resume" {
		t.Fatalf("responses = %#v", responses)
	}
}

func TestModelResumePickerUsesConfiguredDensePreviewRows(t *testing.T) {
	now := fixedTeaTime()
	model := NewModel(codextui.NewState(nil), Options{
		Width:             120,
		Height:            24,
		SessionPickerView: "dense",
		SessionPickerItems: []codextui.SessionSummary{{
			ThreadID:  "thread-preview",
			Title:     "Renamed Session",
			Preview:   "请你写一段快速排序的代码使用go",
			CWD:       `D:\repo\a`,
			Branch:    "main",
			Provider:  "openai",
			UpdatedAt: now.Add(-18 * time.Minute),
		}},
	})
	model.now = func() time.Time { return now }

	typeText(t, model, "/resume")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"18m ago", "请你写一段快速排序的代码使用go", "ctrl+o comfy"} {
		if !strings.Contains(view, want) {
			t.Fatalf("dense resume picker missing %q:\n%s", want, view)
		}
	}
	for _, notWant := range []string{"Renamed Session", "cwd:", "provider:"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("dense resume picker should not show %q:\n%s", notWant, view)
		}
	}
}

func TestModelResumeCommandSearchFilterAndDirectMatchRust(t *testing.T) {
	now := fixedTeaTime()
	state := codextui.NewState(nil)
	var responses []ModalResponse
	model := NewModel(state, Options{
		Width:            120,
		Height:           24,
		SessionPickerCWD: `D:\repo\a`,
		SessionPickerItems: []codextui.SessionSummary{
			{
				ThreadID:  "thread-auth",
				Title:     "Investigate auth flow",
				Path:      `D:\codex\sessions\auth.jsonl`,
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
	model.now = func() time.Time { return now.Add(48 * time.Hour) }

	typeText(t, model, "/resume")
	model.Update(key(bubbletea.KeyEnter))
	typeText(t, model, "auth")
	view := model.View()
	if !strings.Contains(view, "Search: auth") || !strings.Contains(view, "Investigate auth flow") {
		t.Fatalf("resume search did not filter/show query:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEsc))
	if view := model.View(); strings.Contains(view, "Search: auth") || !strings.Contains(view, "Type to search") {
		t.Fatalf("Esc should clear resume search first:\n%s", view)
	}
	model.Update(key(bubbletea.KeyRight))
	if view := model.View(); !strings.Contains(view, "Filter:  Cwd [All]") || !strings.Contains(view, "Other Workspace") {
		t.Fatalf("Right should toggle focused filter to All:\n%s", view)
	}
	model.Update(key(bubbletea.KeyTab))
	model.Update(key(bubbletea.KeyRight))
	if view := model.View(); !strings.Contains(view, "Sort:  Updated [Created]") {
		t.Fatalf("Tab+Right should toggle sort toolbar:\n%s", view)
	}
	model.Update(key(bubbletea.KeyCtrlO))
	if view := model.View(); !strings.Contains(view, "ctrl+o dense") {
		t.Fatalf("Ctrl+O should toggle comfortable view footer:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEsc))

	typeText(t, model, "/resume Investigate auth flow")
	model.Update(key(bubbletea.KeyEnter))
	if state.ThreadID != "thread-auth" {
		t.Fatalf("direct resume ThreadID = %q, want thread-auth", state.ThreadID)
	}
	if len(responses) == 0 || responses[len(responses)-1].Picker == nil || responses[len(responses)-1].Picker.Value != "thread-auth" {
		t.Fatalf("direct resume response missing: %#v", responses)
	}
}

func TestModelResumeCommandMissingMatchShowsRustError(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		SessionPickerItems: []codextui.SessionSummary{{
			ThreadID: "thread-auth",
			Title:    "Investigate auth flow",
		}},
	})

	typeText(t, model, "/resume missing")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	if !strings.Contains(view, "No saved chat found matching 'missing'.") {
		t.Fatalf("missing resume error not shown:\n%s", view)
	}
	if state.ThreadID != "" {
		t.Fatalf("ThreadID = %q, want unchanged empty", state.ThreadID)
	}
}

func TestModelAgentCommandLoadsPickerAndSwitchesThread(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	state.AddMessage(codextui.RoleUser, "old main prompt")
	var readCurrent string
	var switched []string
	model := NewModel(state, Options{
		OnReadAgents: func(currentThreadID string) ([]codextui.AgentThreadEntry, error) {
			readCurrent = currentThreadID
			return []codextui.AgentThreadEntry{
				{ThreadID: "thread-main", IsPrimary: true},
				{ThreadID: "thread-worker", AgentNickname: "Scout", AgentRole: "review", IsRunning: true},
			}, nil
		},
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			switched = append(switched, threadID)
			return AgentThreadSwitchResponse{
				Entry: codextui.AgentThreadEntry{
					ThreadID:      threadID,
					AgentNickname: "Scout",
					AgentRole:     "review",
				},
				Messages: []codextui.Message{
					{Role: codextui.RoleUser, Text: "worker prompt"},
					{Role: codextui.RoleAssistant, Text: "worker answer"},
				},
				Status: "idle",
			}, nil
		},
	})

	typeText(t, model, "/agent")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/agent did not request agent list")
	}
	model.Update(cmd())
	if readCurrent != "thread-main" {
		t.Fatalf("read current = %q, want thread-main", readCurrent)
	}
	view := model.View()
	for _, want := range []string{"Subagents", "Main [default] (current)", "Scout [review]", "running", "thread-worker"} {
		if !strings.Contains(view, want) {
			t.Fatalf("agent picker missing %q:\n%s", want, view)
		}
	}

	model.Update(key(bubbletea.KeyDown))
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("agent selection did not request switch")
	}
	model.Update(cmd())
	if len(switched) != 1 || switched[0] != "thread-worker" {
		t.Fatalf("switched = %#v", switched)
	}
	if state.ThreadID != "thread-worker" {
		t.Fatalf("ThreadID = %q, want thread-worker", state.ThreadID)
	}
	if len(state.Messages) != 2 || state.Messages[0].Text != "worker prompt" || state.Messages[1].Text != "worker answer" {
		t.Fatalf("messages = %#v", state.Messages)
	}
	if !strings.Contains(model.View(), "Scout [review]") {
		t.Fatalf("active agent notice missing:\n%s", model.View())
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

func TestModelRequestUserInputModalOtherOptionMatchesRust(t *testing.T) {
	var responses []ModalResponse
	model := NewModel(nil, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.Update(RequestUserInputMsg{
		ID: "user-input-other",
		Questions: []codextui.RequestUserInputQuestion{{
			ID:       "scope",
			Question: "Where should this apply?",
			IsOther:  true,
			Options:  []codextui.RequestUserInputChoice{{Label: "Plan"}, {Label: "All"}},
		}},
	})
	view := model.View()
	for _, want := range []string{"None of the above", "Optionally, add details in notes (tab)."} {
		if !strings.Contains(view, want) {
			t.Fatalf("other modal missing %q:\n%s", want, view)
		}
	}
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyTab))
	typeText(t, model, "custom scope")
	model.Update(key(bubbletea.KeyEnter))
	if len(responses) != 1 || responses[0].UserInput == nil {
		t.Fatalf("responses = %#v", responses)
	}
	if got := responses[0].UserInput.AnswerLists["scope"]; len(got) != 2 || got[0] != codextui.RequestUserInputOtherOptionLabel || got[1] != "user_note: custom scope" {
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

func runTeaCmd(t *testing.T, model *Model, cmd bubbletea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(bubbletea.BatchMsg); ok {
		for _, sub := range batch {
			runTeaCmd(t, model, sub)
		}
		return
	}
	model.Update(msg)
}

func runes(value string) bubbletea.KeyMsg {
	return bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRunes, Runes: []rune(value)})
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

func countMessageText(messages []codextui.Message, role codextui.MessageRole, text string) int {
	count := 0
	for _, message := range messages {
		if message.Role == role && strings.TrimSpace(message.Text) == text {
			count++
		}
	}
	return count
}
