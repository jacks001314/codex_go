package tea

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	appsapi "codex_go/apps"
	"codex_go/appserver"
	"codex_go/config"
	"codex_go/plugin"
	"codex_go/protocol"
	"codex_go/review"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	mentionsv2 "codex_go/tui/bottom_pane/mentions_v2"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
	idecontext "codex_go/tui/ide_context"
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

func TestModelTokenUsageEventUpdatesStatusCard(t *testing.T) {
	window := int64(200000)
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 24})
	model.Update(ThreadEventMsg{Event: protocol.TokenUsageUpdated(protocol.ThreadTokenUsage{
		Total: protocol.Usage{InputTokens: 50000, CachedInputTokens: 10000, OutputTokens: 5000, TotalTokens: 55000},
		Last:  protocol.Usage{TotalTokens: 50000}, ModelContextWindow: &window,
	})})
	card := state.RenderStatusCardWidth(100)
	for _, want := range []string{"45K total", "80% left (50K used / 200K)"} {
		if !strings.Contains(card, want) {
			t.Fatalf("status card missing %q:\n%s", want, card)
		}
	}
}

func TestModelStatusRefreshesRateLimitsInOriginalHistoryCell(t *testing.T) {
	state := codextui.NewState(nil)
	reads := 0
	model := NewModel(state, Options{HasChatGPTAccount: true, OnReadRateLimits: func() ([]codextui.RateLimitStatus, error) {
		reads++
		return []codextui.RateLimitStatus{{Label: "5h", UsedPercent: 92}}, nil
	}})
	typeText(t, model, "/status")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("ChatGPT /status did not request a rate-limit refresh")
	}
	if raw := state.Messages[0].RawText; strings.Contains(raw, "8% left") || !strings.Contains(raw, "refresh requested; run /status again shortly.") {
		t.Fatalf("initial status did not show in-flight refresh state:\n%s", raw)
	}
	model.Update(cmd())
	if reads != 1 || len(state.RateLimits) != 1 || state.RateLimits[0].UsedPercent != 92 {
		t.Fatalf("refreshed limits reads=%d limits=%#v", reads, state.RateLimits)
	}
	if raw := state.Messages[0].RawText; !strings.Contains(raw, "5h limit:") || !strings.Contains(raw, "8% left") || strings.Contains(raw, "refresh requested") {
		t.Fatalf("original status cell was not refreshed in place:\n%s", raw)
	}
}

func TestModelStatusDoesNotRefreshRateLimitsWithoutChatGPTAccount(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{OnReadRateLimits: func() ([]codextui.RateLimitStatus, error) {
		t.Fatal("non-ChatGPT /status unexpectedly refreshed rate limits")
		return nil, nil
	}})
	typeText(t, model, "/status")
	if _, cmd := model.Update(key(bubbletea.KeyEnter)); cmd != nil {
		t.Fatal("non-ChatGPT /status returned a rate-limit refresh command")
	}
}

func TestModelStatusTracksOverlappingRateLimitRefreshesIndependently(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{HasChatGPTAccount: true, OnReadRateLimits: func() ([]codextui.RateLimitStatus, error) {
		return nil, nil
	}})
	first := model.applyStatusCommand()
	second := model.applyStatusCommand()
	if len(model.pendingStatusRateLimitRequests) != 2 {
		t.Fatalf("pending status refreshes = %#v", model.pendingStatusRateLimitRequests)
	}
	firstMessage := first().(RateLimitsResultMsg)
	secondMessage := second().(RateLimitsResultMsg)
	model.applyRateLimitsResult(RateLimitsResultMsg{
		RequestID: firstMessage.RequestID,
		Limits:    []codextui.RateLimitStatus{{Label: "5h", UsedPercent: 10}},
	})
	if len(model.pendingStatusRateLimitRequests) != 1 || len(state.RateLimits) != 1 || state.RateLimits[0].UsedPercent != 10 {
		t.Fatalf("first completion pending=%#v limits=%#v", model.pendingStatusRateLimitRequests, state.RateLimits)
	}
	model.applyRateLimitsResult(RateLimitsResultMsg{RequestID: secondMessage.RequestID})
	if len(model.pendingStatusRateLimitRequests) != 0 || len(state.RateLimits) != 0 || !state.RateLimitsLoaded {
		t.Fatalf("empty completion pending=%#v limits=%#v", model.pendingStatusRateLimitRequests, state.RateLimits)
	}
	if !strings.Contains(state.Messages[0].RawText, "90% left") || !strings.Contains(state.Messages[1].RawText, "not available for this account") {
		t.Fatalf("overlapping status cells were not updated independently: %#v", state.Messages)
	}
}

func TestModelStatusCommandAddsRustStyleHistoryCell(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.4", CWD: `D:\repo`})
	state.SetThreadID("thread-status")
	state.SetThreadName("Status parity")
	model := NewModel(state, Options{SessionHeaderVersion: "0.145.0"})

	typeText(t, model, "/status")
	model.Update(key(bubbletea.KeyEnter))
	if len(state.Messages) != 1 || state.Messages[0].Role != codextui.RoleHistory {
		t.Fatalf("status messages = %#v", state.Messages)
	}
	raw := state.Messages[0].RawText
	for _, want := range []string{"gcode (v0.145.0)", "Thread name:", "Status parity", "Session:", "thread-status"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("status output missing %q:\n%s", want, raw)
		}
	}
	for _, notWant := range []string{"gcode (Go)", "API key configured", "Session:             new"} {
		if strings.Contains(raw, notWant) {
			t.Fatalf("status output contains stale %q:\n%s", notWant, raw)
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
	for _, want := range []string{" ACTIVITY ─", "╭", "╰", "Ask gcode"} {
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
	for _, want := range []string{"gcode", "model:", "gpt-5.5 xhigh", "directory:", `D:\repo`} {
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

	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "\u2022 Working (12s \u2022 esc to interrupt)") {
		t.Fatalf("Working indicator missing or not Rust-like:\n%s", view)
	}
	if strings.Contains(view, "Thinking") {
		t.Fatalf("Working indicator should not render a separate Thinking line:\n%s", view)
	}
	if !strings.Contains(view, "Ask gcode") {
		t.Fatalf("composer should remain visible below Working indicator:\n%s", view)
	}
}

func TestModelWorkingIndicatorHighlightsLettersInSequence(t *testing.T) {
	start := time.Unix(100, 0)
	state := codextui.NewState(nil)
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 18})
	model.taskStartedAt = start
	model.now = func() time.Time { return start }

	first := model.renderWorkingIndicator()
	for range workingHighlightTicksPerLetter {
		model.animEngine.Advance()
	}
	second := model.renderWorkingIndicator()
	if first == second {
		t.Fatalf("Working highlight did not advance to the next letter: %q", first)
	}
	if got := utils.StripANSI(first); got != utils.StripANSI(second) {
		t.Fatalf("animation changed the status text or layout: %q != %q", got, utils.StripANSI(second))
	}
	if !strings.Contains(first, "\x1b[1mW\x1b[0m") {
		t.Fatalf("first frame should highlight W: %q", first)
	}
	if !strings.Contains(second, "\x1b[1mo\x1b[0m") {
		t.Fatalf("second frame should highlight o: %q", second)
	}

	for range workingHighlightTicksPerLetter * (len([]rune("Working")) - 1) {
		model.animEngine.Advance()
	}
	if cycled := model.renderWorkingIndicator(); cycled != first {
		t.Fatalf("Working highlight did not cycle back to W: %q", cycled)
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

	view := utils.StripANSI(model.View())
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

func TestModelKeymapDebugCommandRendersCatalog(t *testing.T) {
	model := NewModel(nil, Options{})

	typeText(t, model, "/keymap debug")
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
			t.Fatalf("/keymap debug catalog missing %q:\n%s", want, catalog)
		}
	}
	if len(model.SubmittedPrompts()) != 0 {
		t.Fatalf("/keymap should not submit prompts: %#v", model.SubmittedPrompts())
	}
}

func TestModelKeymapCommandRejectsNonRustArguments(t *testing.T) {
	model := NewModel(nil, Options{})

	typeText(t, model, "/keymap list")
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Usage: /keymap [debug]") {
		t.Fatalf("invalid /keymap argument missing Rust usage error:\n%s", view)
	}
	if len(model.SubmittedPrompts()) != 0 {
		t.Fatalf("recognized /keymap inline command should not submit a prompt: %#v", model.SubmittedPrompts())
	}
}

func TestModelKeymapCommandOpensGuidedPickerAndCapturesKey(t *testing.T) {
	var edits []codextui.KeymapEdit
	model := NewModel(nil, Options{
		OnKeymapEdit: func(edit codextui.KeymapEdit) (*codextui.KeymapConfig, string, error) {
			edits = append(edits, edit)
			next := modelKeymapConfigAfterEdit(t, nil, edit)
			return next, "saved keymap", nil
		},
	})

	typeText(t, model, "/keymap")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.id != chatwidget.KeymapPickerViewID {
		t.Fatalf("/keymap modal = %#v", model.modal)
	}
	model.modal.selected = modalOptionIndexByID(t, model.modal.options, "global:open_transcript")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.id != chatwidget.KeymapActionMenuViewID {
		t.Fatalf("action modal = %#v", model.modal)
	}
	model.modal.selected = modalOptionIndexByID(t, model.modal.options, "set")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.keymapCapture == nil {
		t.Fatalf("capture modal = %#v", model.modal)
	}
	model.Update(key(bubbletea.KeyCtrlK))
	if len(edits) != 1 || edits[0].Context != "global" || edits[0].Action != "open_transcript" || !reflect.DeepEqual(edits[0].Bindings, []string{"ctrl-k"}) {
		t.Fatalf("guided keymap edits = %#v", edits)
	}
	if model.modal == nil || model.modal.id != chatwidget.KeymapPickerViewID || model.notice != "saved keymap" {
		t.Fatalf("post-capture modal=%#v notice=%q", model.modal, model.notice)
	}
}

func modalOptionIndexByID(t *testing.T, options []ModalOption, id string) int {
	t.Helper()
	for index, option := range options {
		if option.ID == id {
			return index
		}
	}
	t.Fatalf("modal option %q missing from %#v", id, options)
	return -1
}

func modelKeymapConfigAfterEdit(t *testing.T, current *codextui.KeymapConfig, edit codextui.KeymapEdit) *codextui.KeymapConfig {
	t.Helper()
	next := current.Clone()
	var err error
	if edit.Operation == codextui.KeymapEditUnset {
		err = next.Unset(edit.Context, edit.Action)
	} else {
		err = next.Set(edit.Context, edit.Action, edit.Bindings)
	}
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestModelKeymapEditAppliesRuntimeRemap(t *testing.T) {
	var seeds []string
	model := NewModel(nil, Options{
		OnExternalEditor: func(seed string) bubbletea.Cmd {
			seeds = append(seeds, seed)
			return nil
		},
	})

	next, _, err := model.applyKeymapEdit(codextui.KeymapEdit{
		Operation: codextui.KeymapEditSet,
		Context:   "global",
		Action:    "open_external_editor",
		Bindings:  []string{"ctrl-e"},
	})
	if err != nil {
		t.Fatal(err)
	}
	model.keymapConfig = next

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

func TestModelDisablePasteBurstSubmitsOnEnterLikeRust(t *testing.T) {
	// Rust disable_paste_burst (chat_composer.rs): when true, burst detection
	// is bypassed entirely - fast typed input does not suppress Enter, so a
	// burst of characters followed by Enter submits the prompt instead of
	// inserting a newline.
	now := time.Unix(0, 0)
	var requests []SubmitRequest
	model := NewModel(nil, Options{
		DisablePasteBurst: true,
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})
	model.now = func() time.Time { return now }

	model.Update(runes("hello"))
	now = now.Add(10 * time.Millisecond)
	model.Update(key(bubbletea.KeyEnter))
	if len(requests) != 1 {
		t.Fatalf("requests after burst Enter = %#v, want submit (disable_paste_burst)", requests)
	}
	if requests[0].Prompt != "hello" {
		t.Fatalf("submitted prompt = %q, want hello", requests[0].Prompt)
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
	if countRole(model.State.Messages, codextui.RoleHistory) != 1 {
		t.Fatalf("history messages = %#v, want one status message", model.State.Messages)
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
		OnSteerRequest: func(SubmitRequest, string) error {
			t.Fatal("explicit queue invoked steer")
			return nil
		},
	})

	typeText(t, model, "queued while busy")
	model.Update(key(bubbletea.KeyTab))
	if len(requests) != 0 {
		t.Fatalf("requests before completion = %#v", requests)
	}
	if got := model.QueuedRequests(); len(got) != 1 || got[0].Prompt != "queued while busy" {
		t.Fatalf("queued requests = %#v", got)
	}
	if !strings.Contains(model.View(), "Queued follow-up inputs") || !strings.Contains(model.View(), "queued while busy") {
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
	if strings.Contains(model.View(), "Queued follow-up inputs") || strings.Contains(model.View(), "Queued: queued while busy") {
		t.Fatalf("queued preview remained after completion:\n%s", model.View())
	}
	if state.Status != "running" {
		t.Fatalf("status after queued submit = %q, want running", state.Status)
	}
}

func TestModelSteersPromptWhileRunningAndCommitsWithoutFutureQueue(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	var steered SubmitRequest
	var clientID string
	model := NewModel(state, Options{
		OnSubmitRequest: func(SubmitRequest) bubbletea.Cmd {
			t.Fatal("running submission started a future turn")
			return nil
		},
		OnSteerRequest: func(request SubmitRequest, id string) error {
			steered = request
			clientID = id
			return nil
		},
	})
	model.composer.SetValue("change direction")
	_, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if cmd == nil {
		t.Fatal("steer command is nil")
	}
	message := cmd()
	if result, ok := message.(SteerResultMsg); !ok || result.Err != nil {
		t.Fatalf("steer result = %#v", message)
	}
	if steered.Prompt != "change direction" || clientID == "" {
		t.Fatalf("steer request = %#v, clientID = %q", steered, clientID)
	}
	if got := model.QueuedRequests(); len(got) != 0 {
		t.Fatalf("future queue = %#v", got)
	}
	if view := model.View(); !strings.Contains(view, "Messages to be submitted after next tool call") || !strings.Contains(view, "change direction") {
		t.Fatalf("pending steer preview missing:\n%s", view)
	}
	model.Update(SteerCommittedMsg{Count: 1})
	if view := model.View(); strings.Contains(view, "Messages to be submitted after next tool call") {
		t.Fatalf("pending steer preview remained:\n%s", view)
	}
	if countRole(model.State.Messages, codextui.RoleUser) != 1 || model.State.Messages[len(model.State.Messages)-1].Text != "change direction" {
		t.Fatalf("committed steer history = %#v", model.State.Messages)
	}
}

func TestModelRejectedSteerFallsBackAtTurnCompletion(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetStatus("running")
	var submitted []SubmitRequest
	model := NewModel(state, Options{
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
		OnSteerRequest: func(SubmitRequest, string) error {
			return errors.New("no active turn to steer")
		},
	})
	model.composer.SetValue("late steer")
	_, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	result := cmd().(SteerResultMsg)
	model.Update(result)
	if view := model.View(); !strings.Contains(view, "Messages to be submitted at end of turn") || !strings.Contains(view, "late steer") {
		t.Fatalf("rejected steer preview missing:\n%s", view)
	}
	_, completion := model.Update(TurnCompletedMsg{})
	if completion != nil {
		message := completion()
		if batch, ok := message.(bubbletea.BatchMsg); ok {
			for _, command := range batch {
				if command != nil {
					command()
				}
			}
		}
	}
	if len(submitted) != 1 || submitted[0].Prompt != "late steer" {
		t.Fatalf("fallback submissions = %#v", submitted)
	}
	if strings.Contains(model.View(), "Messages to be submitted at end of turn") {
		t.Fatalf("rejected steer preview remained:\n%s", model.View())
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
	if strings.Contains(view, "Ask gcode") {
		t.Fatalf("overlay should hide composer:\n%s", view)
	}
	if !model.overlay.AtBottom() || model.overlay.YOffset() <= 0 {
		t.Fatalf("overlay initial offset=%d atBottom=%v, want scrollable bottom", model.overlay.YOffset(), model.overlay.AtBottom())
	}
	if !batchContainsMessageType(openCmd, bubbletea.EnterAltScreen()) {
		t.Fatal("opening transcript overlay did not enter the alternate screen")
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
	if !batchContainsMessageType(cmd, bubbletea.ExitAltScreen()) {
		t.Fatal("closing transcript overlay did not leave the alternate screen")
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

func TestModelTranscriptOverlayExpandsHistoryRawText(t *testing.T) {
	state := codextui.NewState(nil)
	state.AddHistoryLines(
		[]string{
			"Ran command",
			"  first line",
			"  +2 lines (ctrl + t to view transcript)",
			"  last line",
		},
		[]string{
			"$ command",
			"first line",
			"hidden detail one",
			"hidden detail two",
			"last line",
		},
	)
	model := NewModel(state, Options{Width: 60, Height: 10})

	regular := renderTranscript(state, false, 60, model.activeTUITheme())
	if !strings.Contains(regular, "ctrl + t to view transcript") || strings.Contains(regular, "hidden detail one") {
		t.Fatalf("regular transcript did not stay collapsed:\n%s", regular)
	}

	updated, _ := model.Update(key(bubbletea.KeyCtrlT))
	model = updated.(*Model)
	content := model.overlay.Content()
	for _, want := range []string{"$ command", "hidden detail one", "hidden detail two", "last line"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expanded transcript missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "ctrl + t to view transcript") {
		t.Fatalf("expanded transcript retained collapsed-output hint:\n%s", content)
	}

	state.Messages[0].RawText += "\nlate running detail"
	_ = model.View()
	if content := model.overlay.Content(); !strings.Contains(content, "late running detail") {
		t.Fatalf("expanded transcript did not follow updated raw history:\n%s", content)
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
	if !strings.Contains(model.View(), "Copied last message to clipboard") {
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
	if !strings.Contains(model.View(), "No agent response to copy") {
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
	if !strings.Contains(model.View(), "Copied last message to clipboard") {
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
	if rich := renderTranscript(state, false, 80, model.activeTUITheme()); !strings.Contains(rich, "• final answer") || !strings.Contains(rich, "Tool call") {
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
	if strings.Contains(view, "Ask gcode") {
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
	if !strings.Contains(model.View(), "`/diff` \u2014 _not inside a git repository_") {
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

func TestModelKeepsWeatherCommentaryBeforeCommand(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-commentary", "我先查询河北各城市天气。")})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.AgentMessageItemWithPhase("msg-commentary", "我先查询河北各城市天气。", "commentary"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.CommandExecutionItem("call-weather", "curl weather", "", nil, "in_progress"))})

	view := utils.StripANSI(model.View())
	commentaryAt := strings.Index(view, "我先查询河北各城市天气。")
	commandAt := strings.Index(view, "curl weather")
	if commentaryAt < 0 || commandAt < 0 || commentaryAt >= commandAt {
		t.Fatalf("commentary must render before command:\n%s", view)
	}
	if got := strings.Count(view, "我先查询河北各城市天气。"); got != 1 {
		t.Fatalf("commentary count = %d, want 1:\n%s", got, view)
	}
}

func TestModelRendersWebSearchLifecycleLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 30})

	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.WebSearchItem("search-1", "", map[string]any{"type": "other"}))})
	started := utils.StripANSI(model.View())
	if !strings.Contains(started, "Searching the web") || strings.Contains(started, "Searched the web") {
		t.Fatalf("started web search lifecycle missing or completed early:\n%s", started)
	}

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.WebSearchItem("search-1", "Yunnan weather", map[string]any{
		"type":  "search",
		"query": "Yunnan weather",
	}))})
	completed := utils.StripANSI(model.View())
	if !strings.Contains(completed, "Searched the web for Yunnan weather") {
		t.Fatalf("completed web search lifecycle missing query:\n%s", completed)
	}
	if strings.Contains(completed, "Searching the web") {
		t.Fatalf("completed web search retained stale running row:\n%s", completed)
	}
	if got := strings.Count(completed, "Searched the web"); got != 1 {
		t.Fatalf("completed web search count = %d, want 1:\n%s", got, completed)
	}
}

func TestModelHidesWebCitationsFromAssistantView(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 30})
	citation := "21°C\uE200cite\uE202turn0forecast0\uE201"

	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("weather-answer", "| Kunming | "+citation+" |\n")})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.AgentMessageItem("weather-answer", "| Kunming | "+citation+" |\n"))})

	view := utils.StripANSI(model.View())
	if strings.Contains(view, "turn0forecast0") || strings.Contains(view, "\uE200cite\uE202") {
		t.Fatalf("assistant view leaked provider citation:\n%s", view)
	}
	if !strings.Contains(view, "21°C") {
		t.Fatalf("assistant view lost visible weather value:\n%s", view)
	}
}

func TestModelWeatherLifecycleShowsRunningAndNoDuplicateResults(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 30})
	commentary := "我先查询河北各城市天气。"
	final := "天气查询完成。"

	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-commentary", commentary)})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.CommandExecutionItem("call-weather", "curl weather", "", nil, "in_progress"))})
	running := utils.StripANSI(model.View())
	if !strings.Contains(running, commentary) || !strings.Contains(running, "Running curl weather") {
		t.Fatalf("running lifecycle missing commentary or command:\n%s", running)
	}

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.AgentMessageItemWithPhase("msg-commentary", commentary, "commentary"))})
	exitCode := 0
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.CommandExecutionItem("call-weather", "curl weather", "sunny\n", &exitCode, "completed"))})
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-final", final)})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.AgentMessageItemWithPhase("msg-final", final, "final_answer"))})
	model.Update(ThreadEventMsg{Event: protocol.TurnCompleted(protocol.Usage{})})

	view := utils.StripANSI(model.View())
	for text, want := range map[string]int{commentary: 1, "sunny": 1, final: 1} {
		if got := strings.Count(view, text); got != want {
			t.Fatalf("%q count = %d, want %d:\n%s", text, got, want, view)
		}
	}
	if strings.Index(view, commentary) >= strings.Index(view, "Ran curl weather") || strings.Index(view, "Ran curl weather") >= strings.Index(view, final) {
		t.Fatalf("lifecycle order is not commentary -> command -> final:\n%s", view)
	}
}

func TestModelWeatherFailedCommandLifecycleMatchesRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 30})
	commentary := "我会只执行一次天气查询。"
	command := "curl -sS --max-time 5 https://sdk-weather.invalid/Yunnan"
	failure := "curl: (6) Could not resolve host: sdk-weather.invalid\n"
	final := "无法获取云南实时天气。"

	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-commentary", commentary)})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.CommandExecutionItem("call-weather", command, "", nil, "in_progress"))})
	if view := utils.StripANSI(model.View()); !strings.Contains(view, commentary) || !strings.Contains(view, "Running "+command) {
		t.Fatalf("failed weather command did not expose running lifecycle:\n%s", view)
	}

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.AgentMessageItemWithPhase("msg-commentary", commentary, "commentary"))})
	exitCode := 6
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.CommandExecutionItem("call-weather", command, failure, &exitCode, "failed"))})
	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("msg-final", final)})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.AgentMessageItemWithPhase("msg-final", final, "final_answer"))})
	model.Update(ThreadEventMsg{Event: protocol.TurnCompleted(protocol.Usage{})})

	view := utils.StripANSI(model.View())
	for text, want := range map[string]int{commentary: 1, failure[:len(failure)-1]: 1, final: 1} {
		if got := strings.Count(view, text); got != want {
			t.Fatalf("%q count = %d, want %d:\n%s", text, got, want, view)
		}
	}
}

func TestModelRendersFileChangeSuccessAndFailureLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{CWD: `C:\work`})
	model := NewModel(state, Options{Width: 80, Height: 24})
	changes := []protocol.FileChange{{Path: `C:\work\a.txt`, Kind: "update", Diff: "@@\n-old\n+new"}}

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.FileChangeItem("patch-1", changes, "in_progress"))})
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "a.txt") || !strings.Contains(view, "(+1 -1)") {
		t.Fatalf("started file change missing summary:\n%s", view)
	}

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.FileChangeItemWithOutput("patch-1", changes, "failed", "", "apply_patch verification failed: bad context"))})
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "Failed to apply patch") || !strings.Contains(view, "apply_patch verification failed: bad context") {
		t.Fatalf("failed file change missing Rust-style failure cell:\n%s", view)
	}
}

func TestModelRendersSuccessfulFileChangeLifecycleOnce(t *testing.T) {
	state := codextui.NewState(&codextui.Options{CWD: `/work`})
	model := NewModel(state, Options{Width: 80, Height: 24})
	changes := []protocol.FileChange{{Path: `/work/quicksort.java`, Kind: "add", Diff: "+class quicksort {}\n"}}

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.FileChangeItem("patch-1", changes, "in_progress"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.FileChangeItem("patch-1", changes, "completed"))})

	view := utils.StripANSI(model.View())
	if got := strings.Count(view, "Added quicksort.java"); got != 1 {
		t.Fatalf("successful file change rendered %d times, want 1:\n%s", got, view)
	}
}

func TestModelRendersCompletedOnlyFileChange(t *testing.T) {
	state := codextui.NewState(&codextui.Options{CWD: `/work`})
	model := NewModel(state, Options{Width: 80, Height: 24})
	changes := []protocol.FileChange{{Path: `/work/quicksort.java`, Kind: "add", Diff: "+class quicksort {}\n"}}

	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.FileChangeItem("patch-1", changes, "completed"))})

	if view := utils.StripANSI(model.View()); strings.Count(view, "Added quicksort.java") != 1 {
		t.Fatalf("completed-only file change missing or duplicated:\n%s", view)
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

func commandExecutionItemWithSource(id string, command string, output string, exitCode *int, status string, source string) protocol.ThreadItem {
	item := protocol.CommandExecutionItem(id, command, output, exitCode, status)
	item.Metadata = map[string]any{"source": source}
	return item
}

func TestModelCompactCommandActivityGroupsSuccessesAndPreservesTranscript(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	exit0 := 0

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(commandExecutionItemWithSource("call-1", "printf first", "", nil, "in_progress", "agent"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(commandExecutionItemWithSource("call-1", "printf first", "first\n", &exit0, "completed", "agent"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(commandExecutionItemWithSource("call-2", "printf second", "", nil, "in_progress", "agent"))})

	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "• Ran 1 command · ctrl + t to view transcript") || !strings.Contains(view, "Running printf second") {
		t.Fatalf("active compact group missing:\n%s", view)
	}
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(commandExecutionItemWithSource("call-2", "printf second", "second\n", &exit0, "completed", "agent"))})
	view = utils.StripANSI(model.View())
	if !strings.Contains(view, "• Ran 2 commands · ctrl + t to view transcript") {
		t.Fatalf("completed compact group missing:\n%s", view)
	}
	if strings.Contains(view, "printf second") {
		t.Fatalf("completed compact group should hide individual commands:\n%s", view)
	}

	model.Update(ThreadEventMsg{Event: protocol.AgentMessageDelta("message-1", "Done")})
	view = model.View()
	if !strings.Contains(view, strings.Repeat("─", 20)) {
		t.Fatalf("assistant output after compact group should use the final-message separator:\n%s", view)
	}
	if got := countRole(state.Messages, codextui.RoleHistory); got != 2 {
		t.Fatalf("history count = %d, want compact cell plus separator", got)
	}
	if raw := state.Messages[0].RawText; !strings.Contains(raw, "$ printf first") || !strings.Contains(raw, "$ printf second") {
		t.Fatalf("compact group must preserve the full transcript: %q", raw)
	}
}

func TestModelCompactCommandActivityGroupsUnifiedExecStartup(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	exit0 := 0

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(commandExecutionItemWithSource("call-agent", "echo agent", "", nil, "in_progress", "agent"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(commandExecutionItemWithSource("call-agent", "echo agent", "agent\n", &exit0, "completed", "agent"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(commandExecutionItemWithSource("call-startup", "echo startup", "", nil, "in_progress", "unifiedExecStartup"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(commandExecutionItemWithSource("call-startup", "echo startup", "startup\n", &exit0, "completed", "unifiedExecStartup"))})

	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "• Ran 2 commands · ctrl + t to view transcript") {
		t.Fatalf("unified exec startup command not grouped:\n%s", view)
	}
	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("history count = %d, want one compact cell", got)
	}
}

func TestModelCompactCommandActivityKeepsFailuresVisible(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	exit0 := 0
	exit1 := 1

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(commandExecutionItemWithSource("call-1", "printf first", "", nil, "in_progress", "agent"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(commandExecutionItemWithSource("call-1", "printf first", "first\n", &exit0, "completed", "agent"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(commandExecutionItemWithSource("call-broken", "printf broken", "", nil, "in_progress", "agent"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(commandExecutionItemWithSource("call-broken", "printf broken", "broken\n", &exit1, "failed", "agent"))})

	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "• Ran 1 command · ctrl + t to view transcript") || !strings.Contains(view, "Ran printf broken") {
		t.Fatalf("failed command should stay visible after the compact group:\n%s", view)
	}
	if strings.Contains(view, "Running printf broken") {
		t.Fatalf("failed command left a stale running cell:\n%s", view)
	}
}

func TestModelCompactCommandActivityFlushesForManualShellAndBoundaries(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	exit0 := 0

	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(commandExecutionItemWithSource("call-1", "printf first", "", nil, "in_progress", "agent"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(commandExecutionItemWithSource("call-1", "printf first", "first\n", &exit0, "completed", "agent"))})
	if model.compactCommandGroup == nil {
		t.Fatal("completed groupable command should seed a compact group")
	}
	// A manual shell command never joins an inactive compact group.
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(commandExecutionItemWithSource("call-manual", "printf manual", "", nil, "in_progress", "userShell"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(commandExecutionItemWithSource("call-manual", "printf manual", "manual\n", &exit0, "completed", "userShell"))})
	if model.compactCommandGroup != nil {
		t.Fatal("manual shell command must flush the compact group")
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "You ran printf manual") {
		t.Fatalf("manual shell command must stay visible:\n%s", view)
	}

	// A turn boundary flushes a completed compact group.
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(commandExecutionItemWithSource("call-2", "printf second", "", nil, "in_progress", "agent"))})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(commandExecutionItemWithSource("call-2", "printf second", "second\n", &exit0, "completed", "agent"))})
	if model.compactCommandGroup == nil {
		t.Fatal("completed groupable command should seed a new compact group")
	}
	model.applyTurnCompleted(TurnCompletedMsg{ThreadID: "thread-1"})
	if model.compactCommandGroup != nil {
		t.Fatal("turn completion must flush the compact group")
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

func TestModelMCPStartupDoesNotBlockConfiguredInput(t *testing.T) {
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
	typeText(t, model, "submitted during startup")
	model.Update(key(bubbletea.KeyEnter))
	if !reflect.DeepEqual(submitted, []string{"submitted during startup"}) || len(model.QueuedRequests()) != 0 {
		t.Fatalf("MCP startup blocked input: submitted=%#v queued=%#v", submitted, model.QueuedRequests())
	}

	_, cmd := model.Update(MCPStartupUpdateMsg{Name: "docs", Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupReady}})
	if len(submitted) != 1 {
		t.Fatalf("MCP update resubmitted input: %#v", submitted)
	}
	if cmd == nil {
		t.Fatal("ready update should schedule MCP startup finish lag")
	}
	if model.mcpStartupGeneration == 0 || !model.mcpStartupFinishPending {
		t.Fatalf("finish lag not pending: generation=%d pending=%v", model.mcpStartupGeneration, model.mcpStartupFinishPending)
	}
	model.Update(mcpStartupFinishAfterLagMsg{Generation: model.mcpStartupGeneration})
	if !reflect.DeepEqual(submitted, []string{"submitted during startup"}) {
		t.Fatalf("MCP finish changed submitted input: %#v", submitted)
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

func TestModelRetryStatusIsSingleTransientActivityRow(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ModelRetryStatusMsg{Message: "Reconnecting... 2/5 (30s • esc to interrupt)\n└ Stream disconnected before completion: stream closed", Active: true})
	if !strings.Contains(model.View(), "Reconnecting... 2/5") {
		t.Fatalf("retry status missing from Activity:\n%s", model.View())
	}
	model.Update(ModelRetryStatusMsg{Message: "Reconnecting... 3/5 (30s • esc to interrupt)\n└ Stream disconnected before completion: stream closed", Active: true})
	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("retry activity should update one row: %#v", state.Messages)
	}
	if !strings.Contains(state.Messages[0].RawText, "3/5") {
		t.Fatalf("retry activity was not updated in place: %#v", state.Messages)
	}

	model.Update(ModelRetryStatusMsg{Active: false})
	if got := countRole(state.Messages, codextui.RoleHistory); got != 0 {
		t.Fatalf("completed retry activity should be removed: %#v", state.Messages)
	}
}

func TestModelCompactionStatusIsAnimatedActivity(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(ModelCompactionStatusMsg{Message: "Compacting context...", Active: true})
	if len(state.Messages) != 1 || !strings.Contains(state.Messages[0].Text, "Compacting context...") {
		t.Fatalf("compaction activity missing: %#v", state.Messages)
	}
	model.Update(ModelCompactionStatusMsg{Active: false})
	if len(state.Messages) != 0 {
		t.Fatalf("compaction activity was not cleared: %#v", state.Messages)
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
	command := "echo ok"
	matcher := "Bash"
	model := NewModel(state, Options{
		Width:            80,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnReadHooks: func(cwd string) (appserver.HookListResponse, error) {
			calls++
			if cwd != `D:\repo` {
				t.Fatalf("cwd = %q, want D:\\repo", cwd)
			}
			return appserver.HookListResponse{Data: []appserver.HookListEntry{{
				CWD: `D:\repo`,
				Hooks: []appserver.HookMetadata{{
					Key:         "hook-1",
					EventName:   appserver.HookEventPreToolUse,
					HandlerType: appserver.HookHandlerCommand,
					Matcher:     &matcher,
					Command:     &command,
					TimeoutSec:  10,
					SourcePath:  `D:\repo\.codex\hooks.json`,
					Source:      appserver.HookSourceProject,
					Enabled:     true,
					TrustStatus: appserver.HookTrustTrusted,
				}},
			}}}, nil
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
	for _, want := range []string{"Hooks", "Lifecycle hooks from config", "PreToolUse", "Before a tool executes"} {
		if !strings.Contains(view, want) {
			t.Fatalf("hooks event view missing %q:\n%s", want, view)
		}
	}
	model.Update(key(bubbletea.KeyEnter))
	view = model.View()
	for _, want := range []string{"PreToolUse hooks", "Matcher", "Bash", "Command", "echo ok", "Trust", "Trusted"} {
		if !strings.Contains(view, want) {
			t.Fatalf("hooks handler view missing %q:\n%s", want, view)
		}
	}
}

func TestModelHooksCommandWithoutReaderMatchesRustLoadFailure(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})
	typeText(t, model, "/hooks")
	model.Update(key(bubbletea.KeyEnter))
	want := "Failed to load hooks: hooks/list is unavailable in this runtime"
	if !strings.Contains(model.View(), want) {
		t.Fatalf("hooks load failure missing %q:\n%s", want, model.View())
	}
	if len(state.Messages) != 1 || !strings.Contains(state.Messages[0].RawText, want) {
		t.Fatalf("hooks load failure history = %#v", state.Messages)
	}
}

func TestModelHooksBrowserWritesRustConfigEdits(t *testing.T) {
	command := "echo ok"
	response := func(status appserver.HookTrustStatus) appserver.HookListResponse {
		return appserver.HookListResponse{Data: []appserver.HookListEntry{{
			CWD: `D:\repo`,
			Hooks: []appserver.HookMetadata{{
				Key:         "hook-1",
				EventName:   appserver.HookEventPreToolUse,
				HandlerType: appserver.HookHandlerCommand,
				Command:     &command,
				SourcePath:  `D:\repo\.codex\hooks.json`,
				Source:      appserver.HookSourceProject,
				Enabled:     true,
				CurrentHash: "sha256:current",
				TrustStatus: status,
			}},
		}}}
	}
	tests := []struct {
		name      string
		status    appserver.HookTrustStatus
		openEvent bool
		action    bubbletea.KeyMsg
		wantField string
		wantValue any
	}{
		{name: "toggle", status: appserver.HookTrustTrusted, openEvent: true, action: key(bubbletea.KeySpace), wantField: "enabled", wantValue: false},
		{name: "trust one", status: appserver.HookTrustUntrusted, openEvent: true, action: runes("t"), wantField: "trusted_hash", wantValue: "sha256:current"},
		{name: "trust all", status: appserver.HookTrustModified, action: runes("t"), wantField: "trusted_hash", wantValue: "sha256:current"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var writes []config.ConfigBatchWriteParams
			model := NewModel(codextui.NewState(nil), Options{
				Width:            100,
				SessionPickerCWD: `D:\repo`,
				OnReadHooks: func(string) (appserver.HookListResponse, error) {
					return response(test.status), nil
				},
				OnWriteHookConfig: func(params config.ConfigBatchWriteParams) error {
					writes = append(writes, params)
					return nil
				},
			})
			typeText(t, model, "/hooks")
			_, cmd := model.Update(key(bubbletea.KeyEnter))
			runTeaCmd(t, model, cmd)
			if test.openEvent {
				model.Update(key(bubbletea.KeyEnter))
			}
			_, cmd = model.Update(test.action)
			runTeaCmd(t, model, cmd)
			if len(writes) != 1 || len(writes[0].Edits) != 1 {
				t.Fatalf("writes = %#v, want one edit", writes)
			}
			write := writes[0]
			if !write.ReloadUserConfig || write.Edits[0].KeyPath != "hooks.state" || write.Edits[0].MergeStrategy != config.MergeUpsert {
				t.Fatalf("write params = %#v", write)
			}
			states, ok := write.Edits[0].Value.(map[string]any)
			if !ok {
				t.Fatalf("hook state value = %#v", write.Edits[0].Value)
			}
			hookState, ok := states["hook-1"].(map[string]any)
			if !ok || !reflect.DeepEqual(hookState[test.wantField], test.wantValue) {
				t.Fatalf("hook state = %#v, want %s=%#v", hookState, test.wantField, test.wantValue)
			}
		})
	}
}

func TestModelHooksBrowserUsesRustWriteFailureMessages(t *testing.T) {
	tests := []struct {
		name      string
		status    appserver.HookTrustStatus
		openEvent bool
		action    bubbletea.KeyMsg
		prefix    string
	}{
		{name: "enable", status: appserver.HookTrustTrusted, openEvent: true, action: key(bubbletea.KeySpace), prefix: "Failed to update hook config: "},
		{name: "trust one", status: appserver.HookTrustUntrusted, openEvent: true, action: runes("t"), prefix: "Failed to trust hook: "},
		{name: "trust all", status: appserver.HookTrustModified, action: runes("t"), prefix: "Failed to trust hooks: "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := "echo ok"
			state := codextui.NewState(nil)
			model := NewModel(state, Options{
				SessionPickerCWD: `D:\repo`,
				OnReadHooks: func(string) (appserver.HookListResponse, error) {
					return appserver.HookListResponse{Data: []appserver.HookListEntry{
						{
							CWD: `D:\repo`,
							Hooks: []appserver.HookMetadata{{
								Key: "hook-1", EventName: appserver.HookEventPreToolUse, HandlerType: appserver.HookHandlerCommand,
								Command: &command, SourcePath: `D:\repo\.codex\hooks.json`, Source: appserver.HookSourceProject,
								Enabled: true, CurrentHash: "sha256:current", TrustStatus: test.status,
							}},
						},
					}}, nil
				},
				OnWriteHookConfig: func(config.ConfigBatchWriteParams) error { return errors.New("denied") },
			})
			typeText(t, model, "/hooks")
			_, cmd := model.Update(key(bubbletea.KeyEnter))
			runTeaCmd(t, model, cmd)
			if test.openEvent {
				model.Update(key(bubbletea.KeyEnter))
			}
			_, cmd = model.Update(test.action)
			runTeaCmd(t, model, cmd)
			want := test.prefix + "denied"
			if len(state.Messages) != 1 || !strings.Contains(state.Messages[0].RawText, want) {
				t.Fatalf("history = %#v, want %q", state.Messages, want)
			}
		})
	}
}

func TestModelHooksBrowserConsumesCtrlDForPageDown(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		SessionPickerCWD: `D:\repo`,
		OnReadHooks: func(string) (appserver.HookListResponse, error) {
			return appserver.HookListResponse{Data: []appserver.HookListEntry{{CWD: `D:\repo`}}}, nil
		},
	})
	typeText(t, model, "/hooks")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.hooksBrowser == nil {
		t.Fatal("hooks browser did not open")
	}
	_, cmd = model.Update(key(bubbletea.KeyCtrlD))
	if cmd != nil {
		t.Fatal("Ctrl+D returned a quit command while hooks browser was open")
	}
	if model.modal == nil || model.modal.hooksBrowser == nil {
		t.Fatal("Ctrl+D closed hooks browser")
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
		OnReadPlugins: func(cwd string, forceRefetch bool) (plugin.PluginListResponse, error) {
			calls++
			if cwd != "" || forceRefetch {
				t.Fatalf("plugin reader got cwd=%q forceRefetch=%v", cwd, forceRefetch)
			}
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
	for _, want := range []string{">> Code review started: check security <<"} {
		if !strings.Contains(view, want) {
			t.Fatalf("review view missing %q:\n%s", want, view)
		}
	}
	if state.Status != "running" {
		t.Fatalf("review status = %q, want running", state.Status)
	}
	for _, notWant := range []string{"Review started.", "target: custom instructions", "turn: review-turn"} {
		if strings.Contains(view, notWant) {
			t.Fatalf("review view contains stale %q:\n%s", notWant, view)
		}
	}
}

func TestModelReviewLifecycleMatchesRustAndRestoresTokenUsage(t *testing.T) {
	window := int64(200000)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	state.TotalTokenUsage = codextui.TokenUsage{TotalTokens: 1200}
	state.LastTokenUsage = codextui.TokenUsage{TotalTokens: 300}
	state.ModelContextWindow = &window
	model := NewModel(state, Options{Width: 80, Height: 24})

	entered := protocol.ThreadItem{ID: "review-turn", Type: "enteredReviewMode", Text: "changes against 'main'"}
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(entered)})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(entered)})
	if count := strings.Count(model.View(), ">> Code review started: changes against 'main' <<"); count != 1 {
		t.Fatalf("review start banner count = %d:\n%s", count, model.View())
	}

	reviewWindow := int64(64000)
	model.Update(ThreadEventMsg{Event: protocol.TokenUsageUpdated(protocol.ThreadTokenUsage{
		Total: protocol.Usage{TotalTokens: 9999}, Last: protocol.Usage{TotalTokens: 8888}, ModelContextWindow: &reviewWindow,
	})})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.ThreadItem{ID: "review-turn", Type: "exitedReviewMode", Text: "review output"})})

	if state.TotalTokenUsage.TotalTokens != 1200 || state.LastTokenUsage.TotalTokens != 300 || state.ModelContextWindow == nil || *state.ModelContextWindow != window {
		t.Fatalf("token usage was not restored: total=%#v last=%#v window=%v", state.TotalTokenUsage, state.LastTokenUsage, state.ModelContextWindow)
	}
	if count := strings.Count(model.View(), "<< Code review finished >>"); count != 1 {
		t.Fatalf("review finish banner count = %d:\n%s", count, model.View())
	}
}

func TestModelReviewCustomPresetOpensPromptAndStartsRuntimeReview(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-review")
	var captured review.StartParams
	model := NewModel(state, Options{
		Width: 80, Height: 24,
		OnStartReview: func(params review.StartParams) (review.StartResponse, error) {
			captured = params
			return review.StartResponse{Turn: review.Turn{ID: "review-turn"}, ReviewThreadID: "thread-review"}, nil
		},
	})
	now := fixedTeaTime()
	model.now = func() time.Time { return now }

	typeText(t, model, "/review")
	model.Update(key(bubbletea.KeyEnter))
	for range 3 {
		model.Update(key(bubbletea.KeyDown))
	}
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Custom review instructions") || !strings.Contains(view, "Type instructions and press Enter") {
		t.Fatalf("custom review prompt missing:\n%s", view)
	}
	typeText(t, model, "focus auth boundaries")
	now = now.Add(100 * time.Millisecond)
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("custom review prompt did not return review/start command")
	}
	runTeaCmd(t, model, cmd)
	if captured.Target.Type != "custom" || captured.Target.Instructions != "focus auth boundaries" {
		t.Fatalf("custom review target = %#v", captured.Target)
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
	if view := model.View(); !strings.Contains(view, "Side from main thread") || !strings.Contains(view, "ctrl + / to switch") {
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
	if state.ThreadID != "thread-parent" {
		t.Fatalf("thread id before background close result = %q, want parent", state.ThreadID)
	}
	runTeaCmd(t, model, cmd)

	if closed.ParentThreadID != "thread-parent" || closed.SideThreadID != "thread-side" {
		t.Fatalf("closed side params = %#v", closed)
	}
	if state.ThreadID != "thread-parent" {
		t.Fatalf("thread id after side close = %q", state.ThreadID)
	}
}

func TestModelSideCloseFailureDoesNotReopenAbandonedSide(t *testing.T) {
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

	if state.ThreadID != "thread-parent" {
		t.Fatalf("thread id after failed background side close = %q, want parent", state.ThreadID)
	}
	if view := model.View(); strings.Contains(view, "Failed to close side conversation thread-side") {
		t.Fatalf("background close failure reopened side state:\n%s", view)
	}
	before := len(state.Messages)
	model.Update(ThreadScopedEventMsg{ThreadID: "thread-side", Event: protocol.ThreadEvent{Type: "item.delta", Delta: &protocol.Delta{Text: "late"}}})
	if len(state.Messages) != before {
		t.Fatalf("late abandoned side event changed main transcript: %#v", state.Messages)
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
	if len(state.Messages) == 0 || !strings.Contains(state.Messages[0].RawText, "'/side' is unavailable before the session starts.") {
		t.Fatalf("missing no-started-thread message: %#v", state.Messages)
	}
}

func TestModelSideCommandsAreUnavailableDuringReview(t *testing.T) {
	for _, command := range []string{"/side", "/btw inspect"} {
		t.Run(command, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.SetThreadID("thread-review")
			calls := 0
			model := NewModel(state, Options{
				Width: 80, Height: 24,
				OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
					calls++
					return SideStartResponse{}, nil
				},
			})
			model.reviewState.IsReviewMode = true
			typeText(t, model, command)
			_, cmd := model.Update(key(bubbletea.KeyEnter))
			if cmd != nil || calls != 0 {
				t.Fatalf("review side command returned cmd=%#v calls=%d", cmd, calls)
			}
			name := strings.Fields(command)[0]
			if view := model.View(); !strings.Contains(view, "'"+name+"' is unavailable while code review is running.") {
				t.Fatalf("missing review rejection:\n%s", view)
			}
		})
	}
}

func TestModelSideRenameUsesEphemeralMessage(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	model := NewModel(state, Options{
		Width: 80, Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
	})
	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	typeText(t, model, "/rename temporary")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd != nil {
		t.Fatalf("side rename returned cmd %#v", cmd)
	}
	if view := model.View(); !strings.Contains(view, SideRenameBlockMessage) {
		t.Fatalf("missing side rename message:\n%s", view)
	}
}

func TestModelInactiveParentCompletionStaysInSide(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-parent")
	state.SetStatus("running")
	model := NewModel(state, Options{
		Width: 80, Height: 24,
		OnStartSide: func(params SideStartParams) (SideStartResponse, error) {
			return SideStartResponse{ParentThreadID: params.ParentThreadID, SideThreadID: "thread-side"}, nil
		},
	})
	typeText(t, model, "/side")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	model.Update(TurnCompletedMsg{ThreadID: "thread-parent", AssistantMessage: "parent finished"})
	if state.ThreadID != "thread-side" || state.Status != "idle" {
		t.Fatalf("inactive completion changed side state thread=%q status=%q", state.ThreadID, state.Status)
	}
	if view := model.View(); !strings.Contains(view, "main finished") || strings.Contains(view, "parent finished") {
		t.Fatalf("inactive parent completion was not isolated:\n%s", view)
	}
	model.Update(key(bubbletea.KeyCtrlC))
	if state.ThreadID != "thread-parent" || !strings.Contains(model.View(), "parent finished") {
		t.Fatalf("parent completion was not restored with parent:\n%s", model.View())
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
		OnReadSkills: func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
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
	for _, want := range []string{"Enable/Disable Skills", "Turn skills on or off", "[x] review (Docs)", "Review code"} {
		if !strings.Contains(view, want) {
			t.Fatalf("skills view missing %q:\n%s", want, view)
		}
	}
}

func TestModelSkillsManageWritesSummarizesAndReloads(t *testing.T) {
	const skillPath = `D:\repo\.codex\skills\review\SKILL.md`
	enabled := true
	readCalls := 0
	writeCalls := 0
	model := NewModel(codextui.NewState(nil), Options{
		Width:            80,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnReadSkills: func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
			if forceReload != (readCalls > 0) {
				t.Fatalf("forceReload = %v on read %d", forceReload, readCalls+1)
			}
			readCalls++
			return appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{{
				CWD: cwd,
				Skills: []appserver.SkillsListEntry{{
					Name: "review", Path: skillPath, Description: "Review code", Enabled: enabled,
				}},
			}}}, nil
		},
		OnWriteSkillEnabled: func(path string, requested bool) (bool, error) {
			writeCalls++
			if path != skillPath {
				t.Fatalf("skill path = %q, want %q", path, skillPath)
			}
			enabled = requested
			return requested, nil
		},
	})

	typeText(t, model, "/skills")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyDown))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)

	_, cmd = model.Update(key(bubbletea.KeySpace))
	if cmd == nil {
		t.Fatal("skill toggle did not return config write command")
	}
	runTeaCmd(t, model, cmd)
	if writeCalls != 1 || enabled {
		t.Fatalf("write calls/enabled = %d/%v, want 1/false", writeCalls, enabled)
	}

	_, cmd = model.Update(key(bubbletea.KeyEsc))
	if cmd == nil {
		t.Fatal("closing skills manager did not force inventory reload")
	}
	runTeaCmd(t, model, cmd)
	if readCalls != 2 {
		t.Fatalf("skills read calls = %d, want initial load plus forced reload", readCalls)
	}
	if model.modal != nil {
		t.Fatalf("skills manager remained open after close")
	}
	if view := model.View(); !strings.Contains(view, "0 skills enabled, 1 skills disabled") {
		t.Fatalf("skills change summary missing:\n%s", view)
	}
}

func TestModelSkillsManageWriteFailureDoesNotCountAsChange(t *testing.T) {
	const skillPath = `D:\repo\.codex\skills\review\SKILL.md`
	model := NewModel(codextui.NewState(nil), Options{
		Width:            80,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnReadSkills: func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
			return appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{{
				CWD:    cwd,
				Skills: []appserver.SkillsListEntry{{Name: "review", Path: skillPath, Enabled: true}},
			}}}, nil
		},
		OnWriteSkillEnabled: func(path string, enabled bool) (bool, error) {
			return false, errors.New("permission denied")
		},
	})

	typeText(t, model, "/skills")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyDown))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	_, cmd = model.Update(key(bubbletea.KeyEsc))
	runTeaCmd(t, model, cmd)

	view := model.View()
	want := "Failed to update skill config for " + skillPath + ": permission denied"
	if !strings.Contains(view, want) {
		t.Fatalf("skills write error missing %q:\n%s", want, view)
	}
	if strings.Contains(view, "skills disabled") {
		t.Fatalf("failed skill write was included in summary:\n%s", view)
	}
}

func TestModelSkillPopupReadsRuntimeInventoryAndInsertsSkill(t *testing.T) {
	state := codextui.NewState(nil)
	calls := 0
	model := NewModel(state, Options{
		Width:            90,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnReadSkills: func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
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
		OnReadSkills: func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
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

func TestModelBareUnifiedMentionResetsFileSearch(t *testing.T) {
	queries := []string{}
	model := NewModel(codextui.NewState(nil), Options{
		SessionPickerCWD: `D:\repo`,
		OnFuzzyFileSearch: func(query string, cwd string, cancellationToken string) (appserver.FuzzyFileSearchResponse, error) {
			queries = append(queries, query)
			if cwd != `D:\repo` || cancellationToken != "tui-mentions-v2" {
				t.Fatalf("search cwd/token = %q/%q", cwd, cancellationToken)
			}
			return appserver.FuzzyFileSearchResponse{}, nil
		},
	})

	_, cmd := model.Update(runes("@"))
	runTeaCmd(t, model, cmd)
	if !reflect.DeepEqual(queries, []string{""}) {
		t.Fatalf("queries = %#v, want one empty reset", queries)
	}
	if model.mentionPopup == nil || model.mentionPopup.Query != "" {
		t.Fatalf("mention popup = %#v", model.mentionPopup)
	}
}

func TestModelReopeningIdenticalUnifiedMentionRestartsFileSearch(t *testing.T) {
	queries := []string{}
	model := NewModel(codextui.NewState(nil), Options{
		OnFuzzyFileSearch: func(query string, cwd string, cancellationToken string) (appserver.FuzzyFileSearchResponse, error) {
			queries = append(queries, query)
			return appserver.FuzzyFileSearchResponse{}, nil
		},
	})
	model.composer.SetValue("@foo @foo")
	model.composer.SetCursor(4)
	runTeaCmd(t, model, model.refreshSkillPopup())
	if !reflect.DeepEqual(queries, []string{"", "foo"}) {
		t.Fatalf("first queries = %#v", queries)
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.mentionPopup != nil {
		t.Fatal("mention popup should close on escape")
	}

	model.composer.SetCursor(len([]rune("@foo @foo")))
	runTeaCmd(t, model, model.refreshSkillPopup())
	if !reflect.DeepEqual(queries, []string{"", "foo", "", "foo"}) {
		t.Fatalf("reopen queries = %#v", queries)
	}
}

func TestModelRestoredUnifiedMentionRestartsFileSearch(t *testing.T) {
	queries := []string{}
	model := NewModel(codextui.NewState(nil), Options{
		OnFuzzyFileSearch: func(query string, cwd string, cancellationToken string) (appserver.FuzzyFileSearchResponse, error) {
			queries = append(queries, query)
			return appserver.FuzzyFileSearchResponse{}, nil
		},
	})

	_, cmd := model.Update(ExternalEditorFinishedMsg{Text: "@foo"})
	runTeaCmd(t, model, cmd)
	if !reflect.DeepEqual(queries, []string{"", "foo"}) {
		t.Fatalf("restored queries = %#v", queries)
	}
	if model.mentionPopup == nil || model.mentionPopup.Query != "foo" {
		t.Fatalf("restored mention popup = %#v", model.mentionPopup)
	}
}

func TestModelOpenUnifiedMentionRefreshesSkillAndPluginCatalog(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	model.composer.SetValue("@")
	model.composer.CursorEnd()
	model.refreshSkillPopup()
	if model.mentionPopup == nil {
		t.Fatal("mention popup did not open")
	}

	model.applySkillsInventoryResult(SkillsInventoryResultMsg{Response: appserver.SkillsListResponse{Skills: []appserver.SkillsListEntry{{
		Name: "docs", Path: "skills/docs/SKILL.md", Enabled: true,
	}}}})
	model.applyMentionPluginInventoryResult(MentionPluginInventoryResultMsg{Response: plugin.PluginListResponse{Plugins: []plugin.PluginSummary{{
		ID: "search@team", Name: "search", DisplayName: "Search", Installed: true, Enabled: true,
	}}}})
	if got := unifiedMentionRowNames(model.mentionPopup.Rows()); !reflect.DeepEqual(got, []string{"Search", "docs"}) {
		t.Fatalf("initial rows = %#v", got)
	}

	model.applySkillsInventoryResult(SkillsInventoryResultMsg{Response: appserver.SkillsListResponse{Skills: []appserver.SkillsListEntry{{
		Name: "review", Path: "skills/review/SKILL.md", Enabled: true,
	}}}})
	model.applyMentionPluginInventoryResult(MentionPluginInventoryResultMsg{Response: plugin.PluginListResponse{Plugins: []plugin.PluginSummary{{
		ID: "calendar@team", Name: "calendar", DisplayName: "Calendar", Installed: true, Enabled: true,
	}}}})
	if got := unifiedMentionRowNames(model.mentionPopup.Rows()); !reflect.DeepEqual(got, []string{"Calendar", "review"}) {
		t.Fatalf("refreshed rows = %#v", got)
	}
}

func unifiedMentionRowNames(rows []mentionsv2.SearchResult) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.DisplayName)
	}
	return out
}

func TestModelSkillsListMenuOpensSkillPopup(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:            90,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		OnReadSkills: func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
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
	if got := model.ComposerValue(); got != "@" {
		t.Fatalf("composer = %q, want @", got)
	}
	runTeaCmd(t, model, cmd)
	view := model.View()
	for _, want := range []string{"OpenAI Docs", "Reference OpenAI docs"} {
		if !strings.Contains(view, want) {
			t.Fatalf("/skills list popup missing %q:\n%s", want, view)
		}
	}
}

func TestModelSkillsListMenuUsesLegacyDollarShortcutWhenMentionsV2Disabled(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:           90,
		Height:          24,
		FeatureSettings: map[string]bool{"mentions_v2": false},
	})
	typeText(t, model, "/skills")
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "press $ to open") {
		t.Fatalf("legacy skills shortcut hint missing:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEnter))
	if got := model.ComposerValue(); got != "$" {
		t.Fatalf("composer = %q, want $", got)
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

func TestModelKeymapActionMenuResponsiveWidthsMatchRust(t *testing.T) {
	custom := false
	view := chatwidget.NewKeymapActionMenuView(chatwidget.KeymapActionItem{
		Action:           "open_transcript",
		Bindings:         []string{"ctrl-t"},
		HasCustomBinding: &custom,
	})
	for _, width := range []int{48, 64, 96} {
		model := NewModel(nil, Options{Width: width, Height: 24})
		model.openSelectionViewModal(ModalKindGeneric, view)
		rendered := ansiSequenceRE.ReplaceAllString(model.renderModal(), "")
		for _, line := range strings.Split(rendered, "\n") {
			if got := codextui.DisplayWidth(line); got > width {
				t.Fatalf("width %d line %q is %d columns", width, line, got)
			}
		}
		if !strings.Contains(rendered, "–  Remove custom binding (disabled)") || strings.Contains(rendered, "3. Remove custom binding") {
			t.Fatalf("width %d disabled gutter mismatch:\n%s", width, rendered)
		}
		if width < 96 {
			if !strings.Contains(rendered, "Replace binding\n     Capture one key and replace `ctrl-t`.") {
				t.Fatalf("width %d should stack selected description:\n%s", width, rendered)
			}
			continue
		}
		var twoColumn bool
		for _, line := range strings.Split(rendered, "\n") {
			if strings.Contains(line, "Replace binding") && strings.Contains(line, "Capture one key and replace `ctrl-t`.") {
				twoColumn = true
			}
		}
		if !twoColumn {
			t.Fatalf("width 96 should keep description in columns:\n%s", rendered)
		}
	}
}

func TestModelSlashCommands(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-before-clear")
	state.SetStatus("running")
	state.AddMessage(codextui.RoleUser, "old")
	model := NewModel(state, Options{})

	model.applyModelSetting("gpt-5")
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

	state.SetThreadID("thread-before-named-clear")
	state.AddMessage(codextui.RoleUser, "old again")
	typeText(t, model, "/clear Release triage")
	model.Update(key(bubbletea.KeyEnter))
	if state.ThreadID != "" || state.ThreadName != "Release triage" || len(state.Messages) != 0 {
		t.Fatalf("named clear state = thread %q name %q messages %d", state.ThreadID, state.ThreadName, len(state.Messages))
	}
	if !strings.Contains(model.View(), "Started a new session named Release triage.") {
		t.Fatalf("View() missing named clear notice:\n%s", model.View())
	}

	typeText(t, model, "/new Follow-up")
	model.Update(key(bubbletea.KeyEnter))
	if state.ThreadName != "Follow-up" {
		t.Fatalf("named new ThreadName = %q, want Follow-up", state.ThreadName)
	}
}

func TestModelRenameCommandOpensPrefilledPromptAndPersists(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-rename")
	state.SetThreadName("Current project title")
	var renamedThreadID string
	var renamedName string
	model := NewModel(state, Options{
		OnRenameThread: func(threadID string, name string) error {
			renamedThreadID = threadID
			renamedName = name
			return nil
		},
	})

	typeText(t, model, "/rename")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	if !strings.Contains(view, "Rename thread") || !strings.Contains(view, "Current project title") || !strings.Contains(view, "Press enter to submit or esc to cancel") {
		t.Fatalf("rename prompt view:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEnter))
	if model.modal != nil {
		t.Fatalf("modal = %#v, want closed after rename", model.modal)
	}
	if renamedThreadID != "thread-rename" || renamedName != "Current project title" {
		t.Fatalf("rename callback = (%q, %q)", renamedThreadID, renamedName)
	}
	if state.ThreadName != "Current project title" || !strings.Contains(model.View(), "Thread renamed to Current project title.") {
		t.Fatalf("renamed state=%q view:\n%s", state.ThreadName, model.View())
	}
}

func TestModelRenameCommandAcceptsInlineName(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-inline-rename")
	var calls int
	model := NewModel(state, Options{
		OnRenameThread: func(threadID string, name string) error {
			calls++
			if threadID != "thread-inline-rename" || name != "Release triage" {
				t.Fatalf("rename callback = (%q, %q)", threadID, name)
			}
			return nil
		},
	})

	typeText(t, model, "/rename Release triage")
	model.Update(key(bubbletea.KeyEnter))
	if calls != 1 || state.ThreadName != "Release triage" {
		t.Fatalf("calls=%d ThreadName=%q", calls, state.ThreadName)
	}
}

func TestModelRenamePromptStartsEmptyAndCanCancel(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-empty-rename")
	model := NewModel(state, Options{
		OnRenameThread: func(threadID string, name string) error {
			t.Fatalf("unexpected rename callback = (%q, %q)", threadID, name)
			return nil
		},
	})

	typeText(t, model, "/rename")
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Name thread") || !strings.Contains(view, "Type a name and press Enter") {
		t.Fatalf("empty rename prompt:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil {
		t.Fatal("empty Enter unexpectedly closed rename prompt")
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.modal != nil {
		t.Fatalf("modal = %#v, want cancelled", model.modal)
	}
}

func TestModelNamedNewSessionPersistsNameWhenThreadStarts(t *testing.T) {
	state := codextui.NewState(nil)
	var renamedThreadID string
	var renamedName string
	model := NewModel(state, Options{
		OnRenameThread: func(threadID string, name string) error {
			renamedThreadID = threadID
			renamedName = name
			return nil
		},
	})

	typeText(t, model, "/new Follow-up")
	model.Update(key(bubbletea.KeyEnter))
	if renamedThreadID != "" || !model.pendingThreadName {
		t.Fatalf("rename before thread start = (%q, %q), pending=%v", renamedThreadID, renamedName, model.pendingThreadName)
	}
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "thread.started", ThreadID: "thread-new"}})
	if renamedThreadID != "thread-new" || renamedName != "Follow-up" || model.pendingThreadName {
		t.Fatalf("rename after thread start = (%q, %q), pending=%v", renamedThreadID, renamedName, model.pendingThreadName)
	}
}

func TestModelLogoutExitsOnlyAfterCleanupSucceeds(t *testing.T) {
	var calls int
	model := NewModel(nil, Options{
		OnLogout: func() error {
			calls++
			return nil
		},
	})

	typeText(t, model, "/logout")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil || calls != 0 {
		t.Fatalf("logout command = %v calls=%d, want asynchronous cleanup", cmd, calls)
	}
	result := cmd()
	if calls != 1 {
		t.Fatalf("logout calls = %d, want 1", calls)
	}
	_, quitCmd := model.Update(result)
	if quitCmd == nil {
		t.Fatal("successful logout did not request exit")
	}
	if _, ok := quitCmd().(bubbletea.QuitMsg); !ok {
		t.Fatalf("successful logout returned %T, want QuitMsg", quitCmd())
	}
}

func TestModelLogoutFailureStaysOpenAndShowsError(t *testing.T) {
	model := NewModel(nil, Options{
		OnLogout: func() error {
			return errors.New("credentials store is locked")
		},
	})

	typeText(t, model, "/logout")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	_, quitCmd := model.Update(cmd())
	if quitCmd != nil {
		if _, ok := quitCmd().(bubbletea.QuitMsg); ok {
			t.Fatal("failed logout unexpectedly requested exit")
		}
	}
	if view := model.View(); !strings.Contains(view, "Logout failed: credentials store is locked") {
		t.Fatalf("logout failure view:\n%s", view)
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
	skip := map[string]bool{
		"exit": true,
		"quit": true,
	}
	for _, frame := range codextui.SlashCommandFrames() {
		if slashPopupHiddenCommand(frame.Name) || skip[frame.Name] {
			continue
		}
		t.Run(frame.Name, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.SetThreadID("thread-visible")
			state.AddMessage(codextui.RoleAssistant, "last response")
			model := NewModel(state, Options{Width: 100, Height: 30})
			beforeView := utils.StripANSI(model.View())
			beforeNotice := model.notice
			beforeModal := model.modal
			beforeSubmitted := len(model.SubmittedRequests())
			beforeMessages := len(model.State.Messages)

			invocation, ok := codextui.ParseCommand("/" + frame.Name)
			if !ok {
				t.Fatalf("ParseCommand failed for /%s", frame.Name)
			}
			runTeaCmd(t, model, model.applyCommand(invocation))

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
	for _, item := range model.slashPopup.Items {
		switch item.Name {
		case "help", "approval", "sandbox", "unarchive", "attach", "image", "url-image", "clear-attachments", "editor", "quit", "btw", "apps":
			t.Fatalf("Rust-aligned default popup contains hidden or Go-only command /%s", item.Name)
		}
		if strings.HasPrefix(item.Name, "debug") {
			t.Fatalf("Rust-aligned default popup contains hidden command /%s", item.Name)
		}
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

	model = NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	typeText(t, model, "/qu")
	if len(model.slashPopup.Items) != 1 || model.slashPopup.Items[0].Name != "quit" {
		t.Fatalf("filtered /qu popup items = %#v, want quit", model.slashPopup.Items)
	}

	model = NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	typeText(t, model, "/bt")
	if len(model.slashPopup.Items) != 1 || model.slashPopup.Items[0].Name != "btw" {
		t.Fatalf("filtered /bt popup items = %#v, want btw", model.slashPopup.Items)
	}

	model = NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24, FeatureSettings: map[string]bool{"collaboration_modes": true}})
	typeText(t, model, "/sub")
	if len(model.slashPopup.Items) != 1 || model.slashPopup.Items[0].Name != "subagents" {
		t.Fatalf("filtered /sub popup items = %#v, want subagents", model.slashPopup.Items)
	}
}

func TestModelSlashInlineArgumentDispatchMatchesRust(t *testing.T) {
	goCompatibilityArgs := map[codextui.Command]bool{
		codextui.CommandApproval: true,
		codextui.CommandSandbox:  true,
		codextui.CommandAttach:   true,
		codextui.CommandImage:    true,
		codextui.CommandURLImage: true,
	}
	for _, frame := range codextui.SlashCommandFrames() {
		invocation, ok := codextui.ParseCommand("/" + frame.Name + " argument")
		if !ok {
			t.Fatalf("ParseCommand(/%s argument) failed", frame.Name)
		}
		want := chatwidget.CommandSupportsInlineArgs(invocation.Command) || goCompatibilityArgs[invocation.Command]
		if got := slashInvocationDispatchable(invocation); got != want {
			t.Fatalf("/%s inline dispatchable = %v, want %v", frame.Name, got, want)
		}
	}

	state := codextui.NewState(nil)
	var requests []SubmitRequest
	model := NewModel(state, Options{OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
		requests = append(requests, request)
		return nil
	}})
	typeText(t, model, "/model gpt-new")
	model.Update(key(bubbletea.KeyEnter))
	if state.Model != "" || len(requests) != 1 || requests[0].Prompt != "/model gpt-new" {
		t.Fatalf("unsupported /model args should submit as a prompt: model=%q requests=%#v", state.Model, requests)
	}

	state = codextui.NewState(nil)
	model = NewModel(state, Options{OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
		t.Fatalf("supported inline slash command submitted as prompt: %#v", request)
		return nil
	}})
	typeText(t, model, "/raw on")
	model.Update(key(bubbletea.KeyEnter))
	if !model.rawOutput {
		t.Fatal("supported /raw on did not dispatch")
	}

	typeText(t, model, "/approval never")
	model.Update(key(bubbletea.KeyEnter))
	if state.ApprovalPolicy != "never" {
		t.Fatalf("Go compatibility /approval args did not dispatch: %q", state.ApprovalPolicy)
	}
}

func TestModelSlashInitSubmitsRustPromptRegardlessOfLoadedInstructions(t *testing.T) {
	state := codextui.NewState(nil)
	state.AgentsSummary = "project-instructions.md"
	var requests []SubmitRequest
	model := NewModel(state, Options{OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
		requests = append(requests, request)
		return nil
	}})

	typeText(t, model, "/init")
	model.Update(key(bubbletea.KeyEnter))

	if len(requests) != 1 || requests[0].Prompt != initCommandPrompt() {
		t.Fatalf("/init requests = %#v", requests)
	}
	for _, want := range []string{
		"Before writing, check whether AGENTS.md already exists",
		"do not overwrite or modify it",
		"200-400 words is optimal",
		"Commit & Pull Request Guidelines",
	} {
		if !strings.Contains(requests[0].Prompt, want) {
			t.Fatalf("/init prompt missing %q:\n%s", want, requests[0].Prompt)
		}
	}
	if got := model.ComposerValue(); got != "" {
		t.Fatalf("composer after /init = %q, want empty", got)
	}
	normalizedPrompt := strings.ReplaceAll(initCommandPrompt(), "\r\n", "\n")
	if got, want := fmt.Sprintf("%x", sha256.Sum256([]byte(normalizedPrompt))), "b1f4f6bba488110435f76970e2be3095209f32cf767f7a438b54eab76163d51a"; got != want {
		t.Fatalf("/init prompt SHA-256 = %s, want Rust source %s", got, want)
	}
}

func TestModelSlashCompactStartsRuntimeClearsUsageAndDrainsQueuedInput(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-compact")
	state.TotalTokenUsage = codextui.TokenUsage{TotalTokens: 1200}
	state.LastTokenUsage = codextui.TokenUsage{TotalTokens: 300}
	window := int64(4096)
	state.ModelContextWindow = &window
	var compactThreadID string
	var requests []SubmitRequest
	model := NewModel(state, Options{
		OnStartCompactCommand: func(threadID string) bubbletea.Cmd {
			compactThreadID = threadID
			return func() bubbletea.Msg { return CompactStartResultMsg{} }
		},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})

	typeText(t, model, "/compact")
	_, compactCmd := model.Update(key(bubbletea.KeyEnter))
	if compactCmd == nil || compactThreadID != "thread-compact" || state.Status != "running" {
		t.Fatalf("compact cmd=%v thread=%q status=%q", compactCmd, compactThreadID, state.Status)
	}
	if !state.TotalTokenUsage.IsZero() || !state.LastTokenUsage.IsZero() || state.ModelContextWindow != nil {
		t.Fatalf("token usage was not cleared: total=%#v last=%#v window=%v", state.TotalTokenUsage, state.LastTokenUsage, state.ModelContextWindow)
	}
	for _, message := range state.Messages {
		if strings.Contains(message.Text, "Compaction requested") {
			t.Fatalf("Go-only compaction history leaked: %#v", state.Messages)
		}
	}

	typeText(t, model, "queued after compact")
	model.Update(key(bubbletea.KeyEnter))
	if got := model.QueuedRequests(); len(got) != 1 || got[0].Prompt != "queued after compact" {
		t.Fatalf("queued requests = %#v", got)
	}
	model.Update(compactCmd())
	if state.Status != "running" || len(requests) != 1 || requests[0].Prompt != "queued after compact" {
		t.Fatalf("post-compact status=%q requests=%#v", state.Status, requests)
	}
}

func TestModelSlashCompactFailureReturnsIdleAndShowsError(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-compact")
	model := NewModel(state, Options{OnStartCompactCommand: func(string) bubbletea.Cmd {
		return func() bubbletea.Msg { return CompactStartResultMsg{Err: errors.New("summary failed")} }
	}})

	typeText(t, model, "/compact")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	model.Update(cmd())

	if state.Status != "idle" || !strings.Contains(utils.StripANSI(model.View()), "Compaction: summary failed") {
		t.Fatalf("status=%q view=%s", state.Status, model.View())
	}
}

func TestModelContextCompactionCompletionAddsRustHistoryMarker(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	model.applyItemCompleted(&protocol.ThreadItem{ID: "compact-item", Type: "contextCompaction"})
	if !strings.Contains(utils.StripANSI(model.View()), "Context compacted") {
		t.Fatalf("compaction marker missing:\n%s", model.View())
	}
}

func TestModelRendersCanonicalMultiAgentLifecycleWithoutCiphertext(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	receivers := []string{}
	prompt := "gAAAA-hidden"
	model.applyItemStarted(&protocol.ThreadItem{ID: "wait", Type: "collab_tool_call", Tool: "wait", ReceiverThreadIDs: &receivers, Prompt: &prompt})
	model.applyItemCompleted(&protocol.ThreadItem{ID: "wait", Type: "collab_tool_call", Tool: "wait", ReceiverThreadIDs: &receivers})
	activity := &protocol.ThreadItem{ID: "spawn", Type: "sub_agent_activity", ActivityKind: "started", AgentPath: "/root/worker"}
	model.applyItemStarted(activity)
	model.applyItemCompleted(activity)
	view := utils.StripANSI(model.View())
	for _, want := range []string{"Waiting for agents", "Finished waiting", "No agents completed yet"} {
		if !strings.Contains(view, want) {
			t.Fatalf("multi-agent view missing %q:\n%s", want, view)
		}
	}
	if got := strings.Count(view, "Started `/root/worker`"); got != 2 {
		t.Fatalf("started activity count = %d, want Rust item started + completed lifecycle:\n%s", got, view)
	}
	if strings.Contains(view, "gAAAA") || strings.Contains(view, "collaboration.spawn_agent") {
		t.Fatalf("raw collaboration data leaked:\n%s", view)
	}
}

func TestModelIDECommandEnablesReportsStatusInjectsAndDisables(t *testing.T) {
	state := codextui.NewState(&codextui.Options{CWD: `D:\repo`})
	var requests []SubmitRequest
	reads := 0
	model := NewModel(state, Options{
		OnReadIDEContext: func(cwd string) (*idecontext.IdeContext, error) {
			reads++
			if cwd != `D:\repo` {
				t.Fatalf("IDE context cwd = %q", cwd)
			}
			return &idecontext.IdeContext{
				ActiveFile: &idecontext.ActiveFile{FileDescriptor: idecontext.FileDescriptor{Path: `D:\repo\main.go`}},
			}, nil
		},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			requests = append(requests, request)
			return nil
		},
	})

	typeText(t, model, "/ide on")
	model.Update(key(bubbletea.KeyEnter))
	if !model.ideContext.Enabled || reads != 1 {
		t.Fatalf("IDE enabled=%v reads=%d", model.ideContext.Enabled, reads)
	}
	view := utils.StripANSI(model.View())
	for _, want := range []string{"IDE context is on.", "Future messages will include", "IDE context"} {
		if !strings.Contains(view, want) {
			t.Fatalf("enabled IDE view missing %q:\n%s", want, view)
		}
	}

	typeText(t, model, "review this")
	model.Update(key(bubbletea.KeyEnter))
	if reads != 2 || len(requests) != 1 || requests[0].IDEContext == nil {
		t.Fatalf("IDE submission reads=%d requests=%#v", reads, requests)
	}
	if requests[0].Prompt != "review this" || state.Messages[len(state.Messages)-1].Text != "review this" {
		t.Fatalf("visible prompt changed: request=%q messages=%#v", requests[0].Prompt, state.Messages)
	}

	model.setStatus("idle")
	typeText(t, model, "/ide off")
	model.Update(key(bubbletea.KeyEnter))
	if model.ideContext.Enabled || reads != 2 || !strings.Contains(model.View(), "IDE context is off.") {
		t.Fatalf("IDE disable enabled=%v reads=%d view=%s", model.ideContext.Enabled, reads, model.View())
	}
}

func TestModelIDECommandFailureAndPromptWarningMatchRust(t *testing.T) {
	reads := 0
	model := NewModel(codextui.NewState(nil), Options{
		OnReadIDEContext: func(string) (*idecontext.IdeContext, error) {
			reads++
			if reads == 1 {
				return &idecontext.IdeContext{}, nil
			}
			return nil, &idecontext.IdeContextError{Kind: idecontext.IdeContextErrorRequestFailed, Message: "request-timeout"}
		},
	})

	typeText(t, model, "/ide status")
	model.Update(key(bubbletea.KeyEnter))
	if reads != 0 || !strings.Contains(model.View(), "IDE context is off.") {
		t.Fatalf("disabled status reads=%d view=%s", reads, model.View())
	}
	typeText(t, model, "/ide on")
	model.Update(key(bubbletea.KeyEnter))
	if !model.ideContext.Enabled || reads != 1 || !strings.Contains(model.View(), "Connected to your IDE.") {
		t.Fatalf("enable result enabled=%v reads=%d view=%s", model.ideContext.Enabled, reads, model.View())
	}

	first := SubmitRequest{Prompt: "first"}
	model.captureIDEContext(&first)
	second := SubmitRequest{Prompt: "second"}
	model.captureIDEContext(&second)
	view := utils.StripANSI(model.View())
	if strings.Count(view, "IDE context was skipped for this message.") != 1 {
		t.Fatalf("IDE prompt warning should render once:\n%s", view)
	}
	raw := ""
	for _, message := range model.State.Messages {
		raw += message.RawText
	}
	if !strings.Contains(raw, "did not answer in time") || first.IDEContext != nil || second.IDEContext != nil {
		t.Fatalf("IDE prompt failure result first=%#v second=%#v view=%s", first, second, view)
	}
}

func TestModelIDECommandUnavailableAndInvalidArgs(t *testing.T) {
	reads := 0
	model := NewModel(codextui.NewState(nil), Options{OnReadIDEContext: func(string) (*idecontext.IdeContext, error) {
		reads++
		return nil, &idecontext.IdeContextError{Kind: idecontext.IdeContextErrorConnect}
	}})

	typeText(t, model, "/ide maybe")
	model.Update(key(bubbletea.KeyEnter))
	if reads != 0 || !strings.Contains(model.View(), "Usage: /ide [on|off|status]") {
		t.Fatalf("invalid /ide reads=%d view=%s", reads, model.View())
	}
	typeText(t, model, "/ide")
	model.Update(key(bubbletea.KeyEnter))
	raw := ""
	for _, message := range model.State.Messages {
		raw += message.RawText
	}
	if reads != 1 || model.ideContext.Enabled || !strings.Contains(model.View(), "IDE context could not be enabled.") || !strings.Contains(raw, idecontext.OpenIDEHint) {
		t.Fatalf("unavailable /ide enabled=%v reads=%d view=%s", model.ideContext.Enabled, reads, model.View())
	}
}

func TestModelApproveCommandEmptyAndDeniedSelectionMatchRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-approve")
	var approvedThreadID string
	var approvedEntry chatwidget.AutoReviewDenialEntry
	model := NewModel(state, Options{OnApproveAutoReviewDenial: func(threadID string, entry chatwidget.AutoReviewDenialEntry) error {
		approvedThreadID = threadID
		approvedEntry = entry
		return nil
	}})

	typeText(t, model, "/approve")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal != nil || !strings.Contains(model.View(), "No recent auto-review denials in this thread.") {
		t.Fatalf("empty /approve modal=%#v view=%s", model.modal, model.View())
	}

	raw := json.RawMessage(`{"id":"review-1","turnId":"turn-1","status":"denied","action":{"type":"command","command":"go test ./...","cwd":"D:\\repo"}}`)
	model.Update(GuardianReviewMsg{ThreadID: "thread-approve", Event: chatwidget.GuardianAssessmentEvent{
		ID:        "review-1",
		Status:    chatwidget.GuardianAssessmentDenied,
		Action:    chatwidget.GuardianAssessmentAction{Kind: chatwidget.GuardianActionCommand, Command: "go test ./..."},
		Rationale: "Writes outside the allowed sandbox.",
		Raw:       raw,
	}})
	if len(model.toolRequestRuntime.RecentAutoReviewDenials) != 1 {
		t.Fatalf("auto-review denials = %#v", model.toolRequestRuntime.RecentAutoReviewDenials)
	}

	typeText(t, model, "/approve")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.kind != ModalKindAutoReview || model.modal.selected != 1 {
		t.Fatalf("approve modal = %#v", model.modal)
	}
	view := utils.StripANSI(model.View())
	for _, want := range []string{"Auto-review Denials", "Select a denied action to approve.", "go test ./...", "Writes outside"} {
		if !strings.Contains(view, want) {
			t.Fatalf("approve view missing %q:\n%s", want, view)
		}
	}

	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if approvedThreadID != "thread-approve" || approvedEntry.ID != "review-1" || string(approvedEntry.Event) != string(raw) {
		t.Fatalf("approved thread=%q entry=%#v", approvedThreadID, approvedEntry)
	}
	if len(model.toolRequestRuntime.RecentAutoReviewDenials) != 0 || !strings.Contains(model.View(), "Approval recorded for one retry") {
		t.Fatalf("post-approval denials=%#v view=%s", model.toolRequestRuntime.RecentAutoReviewDenials, model.View())
	}
}

func TestModelApproveCommandReportsCallbackFailure(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-approve")
	model := NewModel(state, Options{OnApproveAutoReviewDenial: func(string, chatwidget.AutoReviewDenialEntry) error {
		return errors.New("rpc unavailable")
	}})
	model.toolRequestRuntime.RecentAutoReviewDenials = []chatwidget.AutoReviewDenialEntry{{
		ID: "review-1", Summary: "curl example.test", Rationale: "Network denied.", Event: json.RawMessage(`{"id":"review-1","status":"denied","action":{"type":"network_access"}}`),
	}}
	typeText(t, model, "/approve")
	model.Update(key(bubbletea.KeyEnter))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if !strings.Contains(model.View(), "Failed to approve auto-review denial: rpc unavailable") {
		t.Fatalf("approval failure missing:\n%s", model.View())
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
	if view := model.View(); !strings.Contains(view, "Enable full access?") || !strings.Contains(view, "Yes, continue anyway") || !strings.Contains(view, "Cancel") {
		t.Fatalf("full access confirmation missing:\n%s", view)
	}
	if view := model.View(); strings.Contains(view, "don't ask again") {
		t.Fatalf("full access confirmation contains obsolete remember choice:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEnter))
	if state.ApprovalPolicy != string(chatwidget.ApprovalNever) || state.Sandbox != chatwidget.DangerFullAccessProfile {
		t.Fatalf("full-access state approval=%q sandbox=%q", state.ApprovalPolicy, state.Sandbox)
	}
	if !strings.Contains(model.View(), "Permissions updated to Full Access") {
		t.Fatalf("permissions history event missing:\n%s", model.View())
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

func TestModelWindowsSandboxStartupPromptMatchesRustFlow(t *testing.T) {
	var calls []chatwidget.WindowsSandboxMode
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 30,
		WindowsSandboxStartupPrompt: &WindowsSandboxStartupPrompt{
			AllowUnelevated: true,
		},
		OnStartWindowsSandboxSetup: func(mode chatwidget.WindowsSandboxMode, cwd string) (WindowsSandboxSetupOutcome, error) {
			calls = append(calls, mode)
			return WindowsSandboxSetupOutcome{
				Started: true,
				Completion: &WindowsSandboxSetupCompletion{
					Mode:    mode,
					Success: true,
				},
			}, nil
		},
	})

	view := model.View()
	for _, want := range []string{
		"Set up the Codex agent sandbox to protect your files and control network access.",
		"Set up default sandbox (requires Administrator permissions)",
		"Use non-admin sandbox (higher risk if prompt injected)",
		"Quit",
		"Press enter to confirm or esc to go back",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("startup prompt missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Select an option") {
		t.Fatalf("startup prompt contains generic title:\n%s", view)
	}

	model.Update(key(bubbletea.KeyDown))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(calls) != 1 || calls[0] != chatwidget.WindowsSandboxModeUnelevated {
		t.Fatalf("setup calls = %#v", calls)
	}
	if !strings.Contains(model.View(), "Windows sandbox setup completed.") {
		t.Fatalf("completion notice missing:\n%s", model.View())
	}
}

func TestModelRequiredWindowsSandboxPromptCannotBeDismissed(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 30,
		WindowsSandboxStartupPrompt: &WindowsSandboxStartupPrompt{
			AllowUnelevated:     false,
			SetupChoiceRequired: true,
		},
	})

	model.Update(key(bubbletea.KeyEsc))
	view := model.View()
	if model.modal == nil || !strings.Contains(view, "Your organization requires the default Codex agent sandbox") {
		t.Fatalf("required prompt was dismissed:\n%s", view)
	}
	if strings.Contains(view, "Use non-admin sandbox") {
		t.Fatalf("required prompt offered disallowed non-admin mode:\n%s", view)
	}
}

func TestModelElevatedWindowsSandboxFailureOpensFallback(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 30,
		WindowsSandboxStartupPrompt: &WindowsSandboxStartupPrompt{
			AllowUnelevated: true,
		},
		OnStartWindowsSandboxSetup: func(mode chatwidget.WindowsSandboxMode, cwd string) (WindowsSandboxSetupOutcome, error) {
			return WindowsSandboxSetupOutcome{
				Started: true,
				Completion: &WindowsSandboxSetupCompletion{
					Mode:    mode,
					Success: false,
					Error:   "elevation denied",
				},
			}, nil
		},
	})

	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	view := model.View()
	for _, want := range []string{
		"Couldn't set up your sandbox with Administrator permissions",
		"Try setting up admin sandbox again",
		"Use Codex with non-admin sandbox",
		"Quit",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("fallback prompt missing %q:\n%s", want, view)
		}
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

func TestModelMCPCommandRefreshesInventory(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width:  90,
		Height: 24,
		MCPServers: []historycell.McpServerStatus{{
			Name:  "stale",
			Tools: []string{"old"},
		}},
		OnReadMCPInventory: func(detail bool) ([]historycell.McpServerStatus, error) {
			if !detail {
				t.Fatal("/mcp verbose did not request full inventory detail")
			}
			return []historycell.McpServerStatus{{
				Name:      "fresh",
				Auth:      "OAuth",
				Tools:     []string{"read"},
				Resources: []historycell.McpResource{{Name: "guide", URI: "file://guide"}},
			}}, nil
		},
	})

	typeText(t, model, "/mcp verbose")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/mcp verbose did not start an inventory request")
	}
	if view := model.View(); !strings.Contains(view, "Loading MCP inventory") || strings.Contains(view, "stale") {
		t.Fatalf("MCP loading state mismatch:\n%s", view)
	}
	model.Update(cmd())
	view := model.View()
	for _, want := range []string{"MCP Tools", "fresh", "Tools: read", "Resources: guide (file://guide)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("refreshed MCP output missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Loading MCP inventory") || strings.Contains(view, "stale") {
		t.Fatalf("refreshed MCP output retained stale/loading state:\n%s", view)
	}
}

func TestModelMCPCommandReportsRefreshFailure(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		OnReadMCPInventory: func(bool) ([]historycell.McpServerStatus, error) {
			return nil, errors.New("offline")
		},
	})

	typeText(t, model, "/mcp")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/mcp did not start an inventory request")
	}
	model.Update(cmd())
	if view := model.View(); !strings.Contains(view, "Failed to load MCP inventory: offline") || strings.Contains(view, "Loading MCP inventory") {
		t.Fatalf("MCP refresh failure mismatch:\n%s", view)
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

	cmd := model.applyPersonalityCommand("pragmatic")
	runTeaCmd(t, model, cmd)
	if len(writes) != 1 || len(writes[0]) != 1 || writes[0][0].KeyPath != "personality" || writes[0][0].Value != "pragmatic" {
		t.Fatalf("personality writes = %#v", writes)
	}
	if state.Personality != string(chatwidget.PersonalityPragmatic) || !strings.Contains(model.View(), "Saved to") {
		t.Fatalf("personality state/view mismatch: state=%q view=\n%s", state.Personality, model.View())
	}

	cmd = model.applyExperimentalCommand("memories off")
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

	cmd := model.applyThemeCommand("dracula")
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
		PetEnv:   map[string]string{"KITTY_WINDOW_ID": "1"},
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
	if model.ComposerValue() != "@" {
		t.Fatalf("skills list should insert @ when mentions_v2 is enabled, got %q", model.ComposerValue())
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
	// Vim mode starts the composer in NORMAL mode (Rust parity); enter insert
	// mode before typing the next slash command so the letters are not
	// dispatched as vim_normal actions.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'i'}})

	typeText(t, model, "/plan investigate architecture")
	model.Update(key(bubbletea.KeyEnter))
	if !state.PlanMode {
		t.Fatalf("PlanMode = false, want true")
	}
	if got := model.SubmittedRequests(); len(got) != 1 || got[0].Prompt != "investigate architecture" {
		t.Fatalf("plan prompt requests = %#v", got)
	}
}

func TestModelPlanCommandAppliesRustCollaborationMode(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.4", ReasoningEffort: "high"})
	state.SetThreadID("thread-plan")
	updatedThreadID := ""
	var updatedMode chatwidget.CollaborationMode
	model := NewModel(state, Options{
		Width:           100,
		Height:          24,
		FeatureSettings: map[string]bool{"collaboration_modes": true},
		OnUpdateCollaborationMode: func(threadID string, mode chatwidget.CollaborationMode) error {
			updatedThreadID = threadID
			updatedMode = mode
			return nil
		},
	})

	typeText(t, model, "/plan investigate architecture")
	model.Update(key(bubbletea.KeyEnter))

	if !state.PlanMode || state.EffectiveReasoningEffort() != chatwidget.CollaborationPlanDefaultReasoningEffort {
		t.Fatalf("plan state = mode %v effort %q", state.PlanMode, state.EffectiveReasoningEffort())
	}
	if updatedThreadID != "thread-plan" || updatedMode.Mode != chatwidget.CollaborationModeKindPlan {
		t.Fatalf("updated collaboration mode thread=%q mode=%#v", updatedThreadID, updatedMode)
	}
	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].CollaborationMode == nil || requests[0].CollaborationMode.Mode != chatwidget.CollaborationModeKindPlan {
		t.Fatalf("plan requests = %#v", requests)
	}
	mode := requests[0].CollaborationMode
	if mode.Settings.ReasoningEffort == nil || *mode.Settings.ReasoningEffort != "medium" || mode.Settings.DeveloperInstructions == nil || !strings.Contains(*mode.Settings.DeveloperInstructions, "## Finalization rule") {
		t.Fatalf("plan request mode = %#v", mode)
	}
	if !strings.Contains(model.View(), "Model changed to gpt-5.4 medium for Plan mode.") {
		t.Fatalf("plan mode change message missing:\n%s", model.View())
	}
}

func TestModelPlanCommandHonorsDisabledCollaborationModes(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 90, Height: 24, FeatureSettings: map[string]bool{"collaboration_modes": false}})
	typeText(t, model, "/plan investigate")
	model.Update(key(bubbletea.KeyEnter))
	if state.PlanMode || len(model.SubmittedRequests()) != 0 {
		t.Fatalf("disabled /plan changed state=%v requests=%#v", state.PlanMode, model.SubmittedRequests())
	}
	if view := model.View(); !strings.Contains(view, "Collaboration modes are disabled.") || !strings.Contains(view, "Enable collaboration modes to use /plan.") {
		t.Fatalf("disabled /plan message missing:\n%s", view)
	}
}

func TestModelRendersProposedPlanLifecycle(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 90, Height: 24})
	model.Update(ThreadEventMsg{Event: protocol.ItemStarted(protocol.PlanItem("plan-1", ""))})
	model.Update(ThreadEventMsg{Event: protocol.PlanDelta("plan-1", "1. Inspect\n")})
	model.Update(ThreadEventMsg{Event: protocol.PlanDelta("plan-1", "2. Verify\n")})
	model.Update(ThreadEventMsg{Event: protocol.ItemCompleted(protocol.PlanItem("plan-1", "1. Inspect\n2. Verify\n"))})

	if len(state.Messages) != 1 || state.Messages[0].Role != codextui.RoleHistory || state.Messages[0].RawText != "1. Inspect\n2. Verify" || !strings.Contains(state.Messages[0].Text, "Proposed Plan") {
		t.Fatalf("proposed plan transcript = %#v", state.Messages)
	}
	if len(model.activeProposedPlans) != 0 {
		t.Fatalf("active proposed plans = %#v", model.activeProposedPlans)
	}
}

func TestModelRuntimeBackedSlashCommands(t *testing.T) {
	state := codextui.NewState(&codextui.Options{CWD: `D:\repo`})
	state.SetThreadID("thread-runtime")
	opened := ""
	granted := ""
	model := NewModel(state, Options{
		OnOpenDesktopThread: func(threadID string) error { opened = threadID; return nil },
		OnReadRolloutPath:   func(threadID string) (string, error) { return `D:\rollouts\thread-runtime.jsonl`, nil },
		OnSandboxReadDir:    func(path string) (string, error) { granted = path; return `D:\canonical-data`, nil },
		FeatureSettings:     map[string]bool{"memories": true, "memory_generation": false},
	})

	for _, command := range []string{"/app", "/rollout", `/sandbox-add-read-dir D:\data`, "/memories"} {
		invocation, ok := codextui.ParseCommand(command)
		if !ok {
			t.Fatalf("ParseCommand(%q) failed", command)
		}
		runTeaCmd(t, model, model.applyCommand(invocation))
	}
	rolloutHistoryFound := false
	for _, message := range state.Messages {
		if message.Role == codextui.RoleHistory && strings.Contains(message.RawText, `Current rollout path: D:\rollouts\thread-runtime.jsonl`) {
			rolloutHistoryFound = true
			break
		}
	}
	if !rolloutHistoryFound {
		t.Fatalf("/rollout did not add a Rust-style info history event: %#v", state.Messages)
	}
	sandboxStartFound := false
	sandboxCompletedFound := false
	for _, message := range state.Messages {
		if message.Role != codextui.RoleHistory {
			continue
		}
		sandboxStartFound = sandboxStartFound || strings.Contains(message.RawText, `Granting sandbox read access to D:\data ...`)
		sandboxCompletedFound = sandboxCompletedFound || strings.Contains(message.RawText, `Sandbox read access granted for D:\canonical-data`)
	}
	if !sandboxStartFound || !sandboxCompletedFound {
		t.Fatalf("sandbox read-root history start=%v completed=%v messages=%#v", sandboxStartFound, sandboxCompletedFound, state.Messages)
	}
	if opened != "thread-runtime" {
		t.Fatalf("opened thread = %q", opened)
	}
	if granted != `D:\data` {
		t.Fatalf("granted path = %q", granted)
	}
	if model.modal == nil || model.modal.kind != ModalKindMemories {
		t.Fatalf("memories modal = %#v", model.modal)
	}
	if !strings.Contains(model.View(), "Memories") {
		t.Fatalf("view missing memories popup:\n%s", model.View())
	}
}

func TestModelExternalAgentImportDetectsSelectsImportsAndRendersResults(t *testing.T) {
	state := codextui.NewState(&codextui.Options{CWD: `D:\repo`})
	item := config.ExternalAgentConfigMigrationItem{
		ItemType:    config.MigrationSkills,
		Description: "Migrate Claude Code skills",
		Details: &config.MigrationDetails{
			Skills: []config.NamedMigration{{Name: "review"}, {Name: "docs"}},
		},
	}
	detectedCWD := ""
	var detectedSources []string
	importedSource := ""
	importedItems := []config.ExternalAgentConfigMigrationItem{}
	completion := make(chan ExternalAgentImportCompletion, 1)
	model := NewModel(state, Options{
		Width:  90,
		Height: 30,
		OnDetectExternalAgent: func(cwd string, source string) (config.ExternalAgentConfigDetectResponse, error) {
			detectedCWD = cwd
			detectedSources = append(detectedSources, source)
			if source != "claude-code" {
				return config.ExternalAgentConfigDetectResponse{}, nil
			}
			return config.ExternalAgentConfigDetectResponse{Items: []config.ExternalAgentConfigMigrationItem{item}}, nil
		},
		OnImportExternalAgent: func(items []config.ExternalAgentConfigMigrationItem, source string) (config.ExternalAgentConfigImportResponse, <-chan ExternalAgentImportCompletion, error) {
			importedSource = source
			importedItems = append(importedItems, items...)
			return config.ExternalAgentConfigImportResponse{ImportID: "import-1"}, completion, nil
		},
	})

	typeText(t, model, "/import")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil || !strings.Contains(model.View(), "Checking for compatible setup") {
		t.Fatalf("import detection state missing:\n%s", model.View())
	}
	runTeaCmd(t, model, cmd)
	if detectedCWD != `D:\repo` {
		t.Fatalf("detected cwd = %q, want D:\\repo", detectedCWD)
	}
	if diff := strings.Join(detectedSources, ","); diff != "claude-code,cursor" {
		t.Fatalf("detected sources = %q", diff)
	}
	view := model.View()
	for _, want := range []string{"Import setup", "Bring over supported setup", "Skills 2", "Selected 1 of 1 item", "Import selected"} {
		if !strings.Contains(view, want) {
			t.Fatalf("import picker missing %q:\n%s", want, view)
		}
	}

	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("import selection did not return import command")
	}
	started := cmd()
	_, completionCmd := model.Update(started)
	if len(importedItems) != 1 || importedItems[0].ItemType != config.MigrationSkills {
		t.Fatalf("imported items = %#v", importedItems)
	}
	if importedSource != "claude-code" {
		t.Fatalf("imported source = %q", importedSource)
	}
	if model.modal != nil {
		t.Fatalf("import modal remained open after success")
	}
	view = model.View()
	for _, want := range []string{"Import started", "Skills: 2 - review, docs"} {
		if !strings.Contains(view, want) {
			t.Fatalf("import start missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Import finished") {
		t.Fatalf("import rendered completion before background result:\n%s", view)
	}
	completion <- ExternalAgentImportCompletion{Completed: config.ExternalAgentConfigImportCompletedNotification{
		ImportID: "import-1",
		ItemTypeResults: []config.ExternalAgentConfigImportTypeResult{{
			ItemType: config.MigrationSkills,
			Successes: []config.ExternalAgentConfigImportItemTypeSuccess{
				{ItemType: config.MigrationSkills},
				{ItemType: config.MigrationSkills},
			},
		}},
	}}
	close(completion)
	runTeaCmd(t, model, completionCmd)
	view = model.View()
	for _, want := range []string{"Import finished: 2 imported, 0 failed", "Run /import again"} {
		if !strings.Contains(view, want) {
			t.Fatalf("import completion missing %q:\n%s", want, view)
		}
	}
}

func TestModelExternalAgentImportNoItemsAndFailureMatchRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		OnDetectExternalAgent: func(cwd string, source string) (config.ExternalAgentConfigDetectResponse, error) {
			return config.ExternalAgentConfigDetectResponse{}, nil
		},
	})
	typeText(t, model, "/import")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if view := model.View(); !strings.Contains(view, "No compatible setup was found to import.") {
		t.Fatalf("no-items message missing:\n%s", view)
	}

	model = NewModel(codextui.NewState(nil), Options{
		OnDetectExternalAgent: func(cwd string, source string) (config.ExternalAgentConfigDetectResponse, error) {
			return config.ExternalAgentConfigDetectResponse{}, errors.New("Import from other apps is unavailable in remote sessions. Start Codex locally and run /import.")
		},
	})
	typeText(t, model, "/import")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if view := model.View(); !strings.Contains(view, "Import from other apps is unavailable in remote sessions") {
		t.Fatalf("import detection failure missing:\n%s", view)
	}
	if model.notice != "Import from other apps is unavailable in remote sessions. Start Codex locally and run /import." {
		t.Fatalf("remote import notice = %q", model.notice)
	}
}

func TestModelExternalAgentImportChoosesBetweenDetectedSources(t *testing.T) {
	claudeItem := config.ExternalAgentConfigMigrationItem{ItemType: config.MigrationConfig, Description: "Claude config"}
	cursorItem := config.ExternalAgentConfigMigrationItem{ItemType: config.MigrationHooks, Description: "Cursor hooks"}
	importedSource := ""
	model := NewModel(codextui.NewState(nil), Options{
		Width: 80, Height: 24,
		OnDetectExternalAgent: func(_ string, source string) (config.ExternalAgentConfigDetectResponse, error) {
			if source == "cursor" {
				return config.ExternalAgentConfigDetectResponse{Items: []config.ExternalAgentConfigMigrationItem{cursorItem}}, nil
			}
			return config.ExternalAgentConfigDetectResponse{Items: []config.ExternalAgentConfigMigrationItem{claudeItem}}, nil
		},
		OnImportExternalAgent: func(items []config.ExternalAgentConfigMigrationItem, source string) (config.ExternalAgentConfigImportResponse, <-chan ExternalAgentImportCompletion, error) {
			importedSource = source
			completion := make(chan ExternalAgentImportCompletion, 1)
			completion <- ExternalAgentImportCompletion{Completed: config.ExternalAgentConfigImportCompletedNotification{ImportID: "import-cursor"}}
			close(completion)
			return config.ExternalAgentConfigImportResponse{ImportID: "import-cursor"}, completion, nil
		},
	})
	typeText(t, model, "/import")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	view := model.View()
	for _, want := range []string{"Choose an import source", "Claude Code", "Cursor", "Press Enter to continue"} {
		if !strings.Contains(view, want) {
			t.Fatalf("source picker missing %q:\n%s", want, view)
		}
	}
	_, _ = model.Update(key(bubbletea.KeyDown))
	_, _ = model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Importing: Hooks 1") {
		t.Fatalf("Cursor migration picker missing:\n%s", view)
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if importedSource != "cursor" {
		t.Fatalf("imported source = %q", importedSource)
	}
}

func TestModelSandboxReadDirUsageAndFailureMatchRustHistory(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		OnSandboxReadDir: func(path string) (string, error) {
			return "", errors.New("access denied")
		},
	})

	usage, ok := codextui.ParseCommand("/sandbox-add-read-dir")
	if !ok {
		t.Fatal("ParseCommand usage failed")
	}
	runTeaCmd(t, model, model.applyCommand(usage))

	failure, ok := codextui.ParseCommand(`/sandbox-add-read-dir D:\private`)
	if !ok {
		t.Fatal("ParseCommand failure failed")
	}
	runTeaCmd(t, model, model.applyCommand(failure))

	want := []string{
		"Usage: /sandbox-add-read-dir <absolute-directory-path>",
		`Granting sandbox read access to D:\private ...`,
		"Error: access denied",
	}
	for _, text := range want {
		found := false
		for _, message := range state.Messages {
			if message.Role == codextui.RoleHistory && strings.Contains(message.RawText, text) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("history missing %q: %#v", text, state.Messages)
		}
	}
}

func TestModelMemoriesEnablePromptPersistsFeatureBeforeNotice(t *testing.T) {
	state := codextui.NewState(nil)
	var writes [][]SettingsEdit
	model := NewModel(state, Options{
		Width:  90,
		Height: 24,
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			return SettingsWriteResult{FeatureSettings: map[string]bool{"memories": true}, FilePath: `D:\codex\config.toml`}, nil
		},
	})

	typeText(t, model, "/memories")
	model.Update(key(bubbletea.KeyEnter))
	for _, want := range []string{"Enable memories?", "Memories are currently disabled", "Yes, enable", "Not now"} {
		if view := model.View(); !strings.Contains(view, want) {
			t.Fatalf("enable prompt missing %q:\n%s", want, view)
		}
	}
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(writes) != 1 || len(writes[0]) != 1 || writes[0][0].KeyPath != "features.memories" || writes[0][0].Value != true {
		t.Fatalf("memory enable writes = %#v", writes)
	}
	if !strings.Contains(model.View(), "Memories will be enabled in the next session.") {
		t.Fatalf("memory enable notice missing:\n%s", model.View())
	}
}

func TestModelMemoriesSettingsToggleAndSaveAtomically(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-memory"
	useMemories := true
	generateMemories := false
	var gotThread string
	var gotUse, gotGenerate, gotChanged bool
	model := NewModel(state, Options{
		Width:            90,
		Height:           24,
		FeatureSettings:  map[string]bool{"memories": true},
		UseMemories:      &useMemories,
		GenerateMemories: &generateMemories,
		OnWriteMemorySettings: func(threadID string, use bool, generate bool, changed bool) (SettingsWriteResult, error) {
			gotThread, gotUse, gotGenerate, gotChanged = threadID, use, generate, changed
			return SettingsWriteResult{UseMemories: &use, GenerateMemories: &generate, FilePath: `D:\codex\config.toml`}, nil
		},
	})

	typeText(t, model, "/memories")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"[x] Use memories", "[ ] Generate memories", "Reset all memories"} {
		if !strings.Contains(view, want) {
			t.Fatalf("memory settings missing %q:\n%s", want, view)
		}
	}
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeySpace))
	if view := model.View(); !strings.Contains(view, "[x] Generate memories") {
		t.Fatalf("generate toggle not reflected:\n%s", view)
	}
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if gotThread != "thread-memory" || !gotUse || !gotGenerate || !gotChanged {
		t.Fatalf("memory write = thread %q use=%v generate=%v changed=%v", gotThread, gotUse, gotGenerate, gotChanged)
	}
	if !strings.Contains(model.View(), "Memory settings saved to") {
		t.Fatalf("memory save result missing:\n%s", model.View())
	}
}

func TestModelMemoriesResetRequiresConfirmationAndRunsCallback(t *testing.T) {
	state := codextui.NewState(nil)
	called := 0
	model := NewModel(state, Options{
		Width:           90,
		Height:          24,
		FeatureSettings: map[string]bool{"memories": true},
		OnResetMemories: func() error {
			called++
			return nil
		},
	})

	typeText(t, model, "/memories")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	for _, want := range []string{"Reset all memories?", "current Codex home", "Go back"} {
		if view := model.View(); !strings.Contains(view, want) {
			t.Fatalf("reset confirmation missing %q:\n%s", want, view)
		}
	}
	if called != 0 {
		t.Fatalf("reset called before confirmation: %d", called)
	}
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if called != 1 || !strings.Contains(model.View(), "Reset local memories.") {
		t.Fatalf("reset result called=%d view=\n%s", called, model.View())
	}
}

func TestModelFeedbackFlowSubmitsOptionalNoteWithoutLogs(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-feedback"
	var submitted appserver.FeedbackUploadParams
	model := NewModel(state, Options{
		Width:  90,
		Height: 28,
		OnSubmitFeedback: func(params appserver.FeedbackUploadParams) (appserver.FeedbackUploadResponse, error) {
			submitted = params
			return appserver.FeedbackUploadResponse{ThreadID: "thread-feedback"}, nil
		},
	})

	typeText(t, model, "/feedback")
	model.Update(key(bubbletea.KeyEnter))
	for _, want := range []string{"How was this?", "bug", "bad result", "good result", "safety check", "other"} {
		if view := model.View(); !strings.Contains(view, want) {
			t.Fatalf("feedback category picker missing %q:\n%s", want, view)
		}
	}
	model.Update(key(bubbletea.KeyEnter))
	for _, want := range []string{"Upload logs?", "codex-logs.log", "codex-doctor-report.json", "Yes", "No"} {
		if view := model.View(); !strings.Contains(view, want) {
			t.Fatalf("feedback consent missing %q:\n%s", want, view)
		}
	}
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Tell us more (bug)") || !strings.Contains(view, "(optional)") {
		t.Fatalf("feedback note view missing:\n%s", view)
	}
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if submitted.Classification != "bug" || submitted.IncludeLogs || submitted.Reason != nil || submitted.ThreadID == nil || *submitted.ThreadID != "thread-feedback" {
		t.Fatalf("feedback params = %#v", submitted)
	}
	view := model.View()
	for _, want := range []string{"Feedback recorded (no logs).", "github.com/openai/codex/issues", "thread-feedback"} {
		if !strings.Contains(view, want) {
			t.Fatalf("feedback result missing %q:\n%s", want, view)
		}
	}
}

func TestModelFeedbackFlowIncludesNoteAndLogs(t *testing.T) {
	var submitted appserver.FeedbackUploadParams
	model := NewModel(codextui.NewState(nil), Options{
		OnSubmitFeedback: func(params appserver.FeedbackUploadParams) (appserver.FeedbackUploadResponse, error) {
			submitted = params
			return appserver.FeedbackUploadResponse{ThreadID: "feedback-1"}, nil
		},
	})
	typeText(t, model, "/feedback")
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyEnter))
	model.Update(key(bubbletea.KeyEnter))
	typeText(t, model, "hung after approval")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if !submitted.IncludeLogs || submitted.Reason == nil || *submitted.Reason != "hung after approval" {
		t.Fatalf("feedback params = %#v", submitted)
	}
	if !strings.Contains(model.View(), "Feedback uploaded.") {
		t.Fatalf("feedback upload result missing:\n%s", model.View())
	}
}

func TestModelFeedbackDisabledByConfiguration(t *testing.T) {
	disabled := false
	model := NewModel(codextui.NewState(nil), Options{FeedbackEnabled: &disabled})
	typeText(t, model, "/feedback")
	model.Update(key(bubbletea.KeyEnter))
	for _, want := range []string{"Sending feedback is disabled", "disabled by configuration", "Close"} {
		if view := model.View(); !strings.Contains(view, want) {
			t.Fatalf("disabled feedback view missing %q:\n%s", want, view)
		}
	}
	model.Update(key(bubbletea.KeyEnter))
	if model.modal != nil {
		t.Fatalf("disabled feedback modal did not close: %#v", model.modal)
	}
}

func TestModelAppCommandUsesRustHistoryMessages(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{})
	typeText(t, model, "/app")
	model.Update(key(bubbletea.KeyEnter))
	if len(state.Messages) != 1 || state.Messages[0].Role != codextui.RoleHistory || !strings.Contains(state.Messages[0].RawText, "Session is still starting; try /app again in a moment.") {
		t.Fatalf("missing-thread messages = %#v", state.Messages)
	}

	state.SetThreadID("thread-app")
	model.onOpenDesktopThread = func(threadID string) error {
		return errors.New("launcher failed")
	}
	typeText(t, model, "/app")
	model.Update(key(bubbletea.KeyEnter))
	want := "Failed to open this session in the Desktop app: launcher failed. Install or launch the Desktop app and try again."
	if got := state.Messages[len(state.Messages)-1]; got.Role != codextui.RoleHistory || !strings.Contains(got.RawText, want) {
		t.Fatalf("open failure message = %#v", got)
	}

	model.onOpenDesktopThread = func(threadID string) error { return nil }
	typeText(t, model, "/app")
	model.Update(key(bubbletea.KeyEnter))
	if got := state.Messages[len(state.Messages)-1]; got.Role != codextui.RoleHistory || !strings.Contains(got.RawText, "Opened this session in the Desktop app.") {
		t.Fatalf("open success message = %#v", got)
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

	_ = model.applyStatusLineCommand("model-with-reasoning current-dir run-state")
	view := model.View()
	for _, want := range []string{"gpt-5", "high", "repo", "idle"} {
		if !strings.Contains(view, want) {
			t.Fatalf("statusline view missing %q:\n%s", want, view)
		}
	}

	_ = model.applyTerminalTitleCommand("app-name project-name run-state")
	if len(writes) != 1 {
		t.Fatalf("terminal title writes = %#v, want one write", writes)
	}
	for _, want := range []string{"\x1b]0;", "codex", "repo", "idle"} {
		if !strings.Contains(writes[0], want) {
			t.Fatalf("terminal title OSC missing %q: %q", want, writes[0])
		}
	}

	_ = model.applyTerminalTitleCommand("off")
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

	_ = model.applyTerminalTitleCommand("does-not-exist")
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

	_ = model.applyStatusLineCommand("model permissions approval-mode")
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

	cmd := model.applyTerminalTitleCommand("app-name model")
	if cmd != nil {
		cmd()
	}
	if len(writes) != 1 || !strings.Contains(writes[0], "\x1b]0;codex | gpt-5\x07") {
		t.Fatalf("terminal title writes = %#v", writes)
	}

	cmd = model.applyTerminalTitleCommand("off")
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
	var storedGoal *appserver.Goal
	model := NewModel(state, Options{
		Width:           100,
		Height:          24,
		StatusLineItems: []string{"task-progress"},
		OnReadGoal: func(threadID string) (*appserver.Goal, error) {
			return cloneGoalTea(storedGoal), nil
		},
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
			goal := appserver.Goal{
				ThreadID:        threadID,
				Objective:       goalObjective,
				TokenBudget:     cloneInt64PtrTea(tokenBudget),
				TokensUsed:      1234,
				TimeUsedSeconds: 90,
				Status:          goalStatus,
			}
			storedGoal = cloneGoalTea(&goal)
			return goal, nil
		},
		OnClearGoal: func(threadID string) (bool, error) {
			clearCalls = append(clearCalls, threadID)
			storedGoal = nil
			return true, nil
		},
	})

	typeText(t, model, "/goal finish parity")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/goal objective did not return preflight command")
	}
	_, cmd = model.Update(cmd())
	if cmd == nil {
		t.Fatal("/goal objective did not return set command")
	}
	model.Update(cmd())
	if len(setCalls) != 1 || setCalls[0].ThreadID != "thread-goal" || setCalls[0].Objective == nil || *setCalls[0].Objective != "finish parity" || setCalls[0].TokenBudget != nil || setCalls[0].Status == nil || *setCalls[0].Status != appserver.GoalActive {
		t.Fatalf("set goal calls = %#v", setCalls)
	}
	if model.currentGoal == nil || model.currentGoal.Objective != "finish parity" {
		t.Fatalf("current goal = %#v", model.currentGoal)
	}
	if raw := state.Messages[len(state.Messages)-1].RawText; !strings.Contains(raw, "Goal active") || !strings.Contains(raw, "Objective: finish parity") {
		t.Fatalf("goal history missing Rust success event:\n%s", raw)
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
	if raw := state.Messages[len(state.Messages)-1].RawText; !strings.Contains(raw, "Goal paused") || !strings.Contains(raw, "Objective: finish parity") {
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
	if raw := state.Messages[len(state.Messages)-1].RawText; !strings.Contains(raw, "Goal cleared") {
		t.Fatalf("clear goal history missing empty state:\n%s", raw)
	}
}

func TestModelGoalSetStartsContinuationWhenActiveAndIdle(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	var continuationCalls []appserver.Goal
	model := NewModel(state, Options{
		Width:           100,
		Height:          24,
		StatusLineItems: []string{"task-progress"},
		OnReadGoal: func(threadID string) (*appserver.Goal, error) {
			return nil, nil
		},
		OnSetGoal: func(threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error) {
			goalStatus := appserver.GoalActive
			if status != nil {
				goalStatus = *status
			}
			return appserver.Goal{
				ThreadID:  threadID,
				Objective: stringPtrValueTea(objective),
				Status:    goalStatus,
			}, nil
		},
		OnGoalContinuation: func(goal appserver.Goal) bubbletea.Cmd {
			continuationCalls = append(continuationCalls, goal)
			return func() bubbletea.Msg { return GoalClearedMsg{ThreadID: "thread-goal"} }
		},
	})

	typeText(t, model, "/goal 1+1")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/goal objective did not return preflight command")
	}
	_, cmd = model.Update(cmd())
	if cmd == nil {
		t.Fatal("/goal objective did not return set command")
	}
	_, cmd = model.Update(cmd())
	if cmd == nil {
		t.Fatal("active goal set did not start continuation command")
	}
	if len(continuationCalls) != 1 {
		t.Fatalf("goal continuation calls = %d, want 1", len(continuationCalls))
	}
	if continuationCalls[0].Objective != "1+1" || continuationCalls[0].Status != appserver.GoalActive {
		t.Fatalf("goal continuation goal = %#v", continuationCalls[0])
	}

	typeText(t, model, "/goal pause")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/goal pause did not return runtime command")
	}
	_, cmd = model.Update(cmd())
	if cmd != nil {
		t.Fatal("paused goal should not start a continuation command")
	}
	if len(continuationCalls) != 1 {
		t.Fatalf("goal continuation calls after pause = %d, want 1", len(continuationCalls))
	}
}

func TestModelGoalContinuationSkippedWhileTurnRunning(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	state.SetStatus("running")
	var continuationCalls int
	model := NewModel(state, Options{
		Width:           100,
		Height:          24,
		StatusLineItems: []string{"task-progress"},
		OnReadGoal: func(threadID string) (*appserver.Goal, error) {
			return nil, nil
		},
		OnSetGoal: func(threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error) {
			return appserver.Goal{
				ThreadID:  threadID,
				Objective: stringPtrValueTea(objective),
				Status:    appserver.GoalActive,
			}, nil
		},
		OnGoalContinuation: func(goal appserver.Goal) bubbletea.Cmd {
			continuationCalls++
			return nil
		},
	})

	typeText(t, model, "/goal 1+1")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("/goal objective did not return preflight command")
	}
	_, cmd = model.Update(cmd())
	if cmd == nil {
		t.Fatal("/goal objective did not return set command")
	}
	_, cmd = model.Update(cmd())
	if cmd != nil {
		t.Fatal("running turn should not start a continuation command")
	}
	if continuationCalls != 0 {
		t.Fatalf("goal continuation calls while running = %d, want 0", continuationCalls)
	}
}

func TestModelGoalContinuesAfterTurnWhenStillActive(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	var reads int
	var continuationCalls int
	stored := &appserver.Goal{ThreadID: "thread-goal", Objective: "1+1", Status: appserver.GoalActive}
	model := NewModel(state, Options{
		Width:           100,
		Height:          24,
		StatusLineItems: []string{"task-progress"},
		OnReadGoal: func(threadID string) (*appserver.Goal, error) {
			reads++
			return cloneGoalTea(stored), nil
		},
		OnGoalContinuation: func(goal appserver.Goal) bubbletea.Cmd {
			continuationCalls++
			return func() bubbletea.Msg { return GoalClearedMsg{ThreadID: "thread-goal"} }
		},
	})
	model.currentGoal = cloneGoalTea(stored)

	_, cmd := model.Update(TurnCompletedMsg{ThreadID: "thread-goal", AssistantMessage: "1 + 1 = 2."})
	if cmd == nil {
		t.Fatal("turn completion with active goal did not return refresh command")
	}
	_, cmd = model.Update(cmd())
	if cmd == nil {
		t.Fatal("active goal refresh did not start continuation command")
	}
	if reads != 1 {
		t.Fatalf("goal refresh reads = %d, want 1", reads)
	}
	if continuationCalls != 1 {
		t.Fatalf("goal continuation calls after turn = %d, want 1", continuationCalls)
	}
}

func TestModelGoalDoesNotContinueAfterTurnWhenCompleted(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	var reads int
	var continuationCalls int
	stored := &appserver.Goal{ThreadID: "thread-goal", Objective: "1+1", Status: appserver.GoalComplete}
	model := NewModel(state, Options{
		Width:           100,
		Height:          24,
		StatusLineItems: []string{"task-progress"},
		OnReadGoal: func(threadID string) (*appserver.Goal, error) {
			reads++
			return cloneGoalTea(stored), nil
		},
		OnGoalContinuation: func(goal appserver.Goal) bubbletea.Cmd {
			continuationCalls++
			return nil
		},
	})
	// The in-memory status is stale (active); the persisted goal is complete.
	model.currentGoal = &appserver.Goal{ThreadID: "thread-goal", Objective: "1+1", Status: appserver.GoalActive}

	_, cmd := model.Update(TurnCompletedMsg{ThreadID: "thread-goal", AssistantMessage: "done"})
	if cmd == nil {
		t.Fatal("turn completion with stale active goal did not return refresh command")
	}
	_, cmd = model.Update(cmd())
	if cmd != nil {
		t.Fatal("completed goal should not start a continuation command")
	}
	if reads != 1 {
		t.Fatalf("goal refresh reads = %d, want 1", reads)
	}
	if continuationCalls != 0 {
		t.Fatalf("goal continuation calls after completion = %d, want 0", continuationCalls)
	}
	if model.currentGoal == nil || model.currentGoal.Status != appserver.GoalComplete {
		t.Fatalf("current goal after refresh = %#v, want complete", model.currentGoal)
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
	if raw := state.Messages[len(state.Messages)-1].RawText; !strings.Contains(raw, "Usage: /goal [<objective>|clear|edit|pause|resume]") || !strings.Contains(raw, "No goal is currently set.") {
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

func TestModelGoalObjectiveUsesRustSyntaxAndReplacementConfirmation(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	existing := appserver.Goal{ThreadID: "thread-goal", Objective: "old objective", Status: appserver.GoalActive}
	var cleared int
	var setParams appserver.GoalSetParams
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnReadGoal: func(string) (*appserver.Goal, error) {
			return cloneGoalTea(&existing), nil
		},
		OnClearGoal: func(string) (bool, error) {
			cleared++
			return true, nil
		},
		OnSetGoal: func(threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error) {
			setParams = appserver.GoalSetParams{ThreadID: threadID, Objective: cloneStringPtrTea(objective), TokenBudget: cloneInt64PtrTea(tokenBudget), Status: cloneGoalStatusPtr(status)}
			return appserver.Goal{ThreadID: threadID, Objective: stringPtrValueTea(objective), Status: *status}, nil
		},
	})

	typeText(t, model, "/goal set finish parity --budget 50000")
	_, readCmd := model.Update(key(bubbletea.KeyEnter))
	if readCmd == nil {
		t.Fatal("goal objective did not start replacement preflight")
	}
	model.Update(readCmd())
	if model.modal == nil || model.modal.kind != ModalKindGoal || model.modal.title != "Replace goal?" || !strings.Contains(model.modal.body, "set finish parity --budget 50000") {
		t.Fatalf("replacement modal = %#v", model.modal)
	}
	_, setCmd := model.Update(key(bubbletea.KeyEnter))
	if setCmd == nil {
		t.Fatal("replacement confirmation did not start goal set")
	}
	model.Update(setCmd())
	if cleared != 1 || setParams.Objective == nil || *setParams.Objective != "set finish parity --budget 50000" || setParams.TokenBudget != nil || setParams.Status == nil || *setParams.Status != appserver.GoalActive {
		t.Fatalf("replacement clear=%d params=%#v", cleared, setParams)
	}
}

func TestModelGoalEditPromptPreservesRustStatusAndBudget(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	budget := int64(80_000)
	existing := appserver.Goal{ThreadID: "thread-goal", Objective: "edit this goal", Status: appserver.GoalPaused, TokenBudget: &budget}
	var setParams appserver.GoalSetParams
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnReadGoal: func(string) (*appserver.Goal, error) {
			return cloneGoalTea(&existing), nil
		},
		OnSetGoal: func(threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error) {
			setParams = appserver.GoalSetParams{ThreadID: threadID, Objective: cloneStringPtrTea(objective), TokenBudget: cloneInt64PtrTea(tokenBudget), Status: cloneGoalStatusPtr(status)}
			return existing, nil
		},
	})

	typeText(t, model, "/goal edit")
	_, readCmd := model.Update(key(bubbletea.KeyEnter))
	model.Update(readCmd())
	if model.modal == nil || model.modal.customPrompt == nil || model.modal.customPrompt.Text != "edit this goal" {
		t.Fatalf("goal edit prompt = %#v", model.modal)
	}
	_, setCmd := model.Update(key(bubbletea.KeyEnter))
	if setCmd == nil {
		t.Fatal("goal edit prompt did not submit")
	}
	model.Update(setCmd())
	if setParams.Status == nil || *setParams.Status != appserver.GoalPaused || setParams.TokenBudget == nil || *setParams.TokenBudget != budget || setParams.Objective == nil || *setParams.Objective != "edit this goal" {
		t.Fatalf("goal edit params = %#v", setParams)
	}
}

func TestModelGoalEditResolvesManagedObjectiveFile(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	reference := "Read the Codex goal objective file at /codex-home/attachments/00000000-0000-4000-8000-000000000000/goal-objective.md before continuing."
	resolved := "original long objective text"
	existing := appserver.Goal{ThreadID: "thread-goal", Objective: reference, Status: appserver.GoalActive}
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnReadGoal: func(string) (*appserver.Goal, error) {
			return cloneGoalTea(&existing), nil
		},
		OnGoalEditText: func(threadID string, objective string) (string, error) {
			if objective != reference {
				t.Fatalf("edit text objective = %q, want reference", objective)
			}
			return resolved, nil
		},
	})

	typeText(t, model, "/goal edit")
	_, readCmd := model.Update(key(bubbletea.KeyEnter))
	model.Update(readCmd())
	if model.modal != nil {
		t.Fatalf("edit prompt should wait for the objective file resolution: %#v", model.modal)
	}
	_, _ = model.Update(GoalEditTextMsg{ThreadID: "thread-goal", Objective: resolved, Goal: existing})
	if model.modal == nil || model.modal.customPrompt == nil || model.modal.customPrompt.Text != resolved {
		t.Fatalf("goal edit prompt = %#v, want resolved text %q", model.modal, resolved)
	}
}

func TestModelGoalWithImageAttachmentMaterializesDraft(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-goal")
	imagePath := filepath.Join(t.TempDir(), "local-image.png")
	if err := os.WriteFile(imagePath, []byte("png bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var materialized []codextui.GoalDraft
	var setObjectives []string
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnReadGoal: func(string) (*appserver.Goal, error) {
			return nil, nil
		},
		OnSetGoal: func(threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error) {
			value := ""
			if objective != nil {
				value = *objective
			}
			setObjectives = append(setObjectives, value)
			return appserver.Goal{ThreadID: threadID, Objective: value, Status: appserver.GoalActive}, nil
		},
		OnGoalDraftMaterialize: func(draft codextui.GoalDraft) (string, error) {
			materialized = append(materialized, draft)
			return "materialized-objective", nil
		},
	})
	model.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: imagePath}}
	typeText(t, model, "/goal describe [Image #1]")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	_, cmd = model.Update(cmd())
	_, cmd = model.Update(cmd())
	model.Update(cmd())
	if len(materialized) != 1 {
		t.Fatalf("draft materialize calls = %d", len(materialized))
	}
	if materialized[0].Objective != "describe [Image #1]" ||
		len(materialized[0].LocalImages) != 1 ||
		materialized[0].LocalImages[0].Placeholder != "[Image #1]" ||
		materialized[0].LocalImages[0].Path != imagePath {
		t.Fatalf("draft = %#v", materialized[0])
	}
	if len(setObjectives) != 1 || setObjectives[0] != "materialized-objective" {
		t.Fatalf("set objectives = %#v", setObjectives)
	}
}

func TestModelGoalWithoutThreadMatchesRustUsageAndQueuesObjective(t *testing.T) {
	state := codextui.NewState(nil)
	readCalls := 0
	model := NewModel(state, Options{
		Width:  100,
		Height: 24,
		OnReadGoal: func(threadID string) (*appserver.Goal, error) {
			readCalls++
			if threadID != "thread-started" {
				t.Fatalf("goal read thread = %q", threadID)
			}
			return nil, nil
		},
	})

	typeText(t, model, "/goal")
	model.Update(key(bubbletea.KeyEnter))
	if raw := state.Messages[len(state.Messages)-1].RawText; !strings.Contains(raw, "Example: /goal improve benchmark coverage") {
		t.Fatalf("bare goal usage = %q", raw)
	}
	typeText(t, model, "/goal pause")
	model.Update(key(bubbletea.KeyEnter))
	if raw := state.Messages[len(state.Messages)-1].RawText; !strings.Contains(raw, "The session must start before you can change a goal.") {
		t.Fatalf("goal control usage = %q", raw)
	}
	typeText(t, model, "/goal queued objective")
	model.Update(key(bubbletea.KeyEnter))
	if model.pendingGoalObjective != "queued objective" {
		t.Fatalf("pending goal objective = %q", model.pendingGoalObjective)
	}
	readCmd := model.applyThreadEvent(protocol.ThreadEvent{Type: "thread.started", ThreadID: "thread-started"})
	if readCmd == nil || model.pendingGoalObjective != "" {
		t.Fatalf("thread-start goal cmd=%v pending=%q", readCmd, model.pendingGoalObjective)
	}
	model.Update(readCmd())
	if readCalls != 1 || model.modal != nil {
		t.Fatalf("queued goal read calls=%d modal=%#v", readCalls, model.modal)
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
	model := NewModel(state, Options{Width: 80, Height: 24, HasChatGPTAccount: true})

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

func TestModelUsageCommandRequiresChatGPTLogin(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 24})

	typeText(t, model, "/usage weekly")
	model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(model.View(), usageChatGPTLoginRequired) {
		t.Fatalf("signed-out usage command missing login error:\n%s", model.View())
	}
	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("history count = %d, want one login error; messages=%#v", got, state.Messages)
	}
}

func TestModelUsageCommandReadsTokenActivity(t *testing.T) {
	state := codextui.NewState(nil)
	lifetime := int64(12345)
	model := NewModel(state, Options{
		Width:             80,
		Height:            24,
		HasChatGPTAccount: true,
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
	zero := int64(0)
	model := NewModel(state, Options{
		Width:                          80,
		Height:                         24,
		HasChatGPTAccount:              true,
		AvailableRateLimitResetCredits: &zero,
	})

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
				Summary: &codextui.SessionSummary{
					ThreadID: "thread-resume",
					Title:    "Resume Me",
					CWD:      `D:\repo\restored`,
				},
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
	_, openCmd := model.Update(key(bubbletea.KeyEnter))
	if !batchContainsMessageType(openCmd, bubbletea.EnterAltScreen()) {
		t.Fatal("opening resume picker did not enter the alternate screen")
	}
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
	if strings.Contains(view, "Thread:") || strings.Contains(view, footerHelpText) {
		t.Fatalf("resume picker should replace the chat surface instead of being appended below it:\n%s", view)
	}
	if first := strings.Split(view, "\n")[0]; !strings.Contains(first, "Resume a previous session") {
		t.Fatalf("resume picker should start at the top of the full-screen surface; first line = %q", first)
	}

	_, closeCmd := model.Update(key(bubbletea.KeyEnter))
	if !batchContainsMessageType(closeCmd, bubbletea.ExitAltScreen()) {
		t.Fatal("selecting a resume target did not leave the alternate screen")
	}
	if state.ThreadID != "thread-resume" {
		t.Fatalf("ThreadID = %q, want thread-resume", state.ThreadID)
	}
	if state.ThreadName != "Resume Me" || state.CWD != `D:\repo\restored` || model.sessionCWD != `D:\repo\restored` {
		t.Fatalf("restored name/cwd = %q/%q (picker %q)", state.ThreadName, state.CWD, model.sessionCWD)
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

func TestModelResumePickerCancelRestoresInlineScreen(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "thread-resume", Preview: "Resume Me"}},
	})
	typeText(t, model, "/resume")
	_, openCmd := model.Update(key(bubbletea.KeyEnter))
	if !batchContainsMessageType(openCmd, bubbletea.EnterAltScreen()) {
		t.Fatal("opening resume picker did not enter the alternate screen")
	}
	_, closeCmd := model.Update(key(bubbletea.KeyEsc))
	if !batchContainsMessageType(closeCmd, bubbletea.ExitAltScreen()) {
		t.Fatal("cancelling resume picker did not leave the alternate screen")
	}
	if model.modal != nil || model.sessionPickerAltScreen {
		t.Fatalf("cancel left picker state active: modal=%#v alt=%v", model.modal, model.sessionPickerAltScreen)
	}
}

func TestModelResumePickerHonorsNoAltScreen(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		NoAltScreen:        true,
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "thread-resume", Preview: "Resume Me"}},
	})
	typeText(t, model, "/resume")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd != nil {
		t.Fatalf("no-alt-screen resume picker returned terminal mode command %T", cmd())
	}
	if !strings.HasPrefix(normalizeTerminalSnapshot(model.View()), "Resume a previous session") {
		t.Fatalf("no-alt-screen picker should still replace the chat surface:\n%s", model.View())
	}
	_, cmd = model.Update(key(bubbletea.KeyEsc))
	if cmd != nil || model.sessionPickerAltScreen {
		t.Fatalf("no-alt-screen cancel returned command/state: cmd=%v alt=%v", cmd != nil, model.sessionPickerAltScreen)
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
	for _, want := range []string{"Subagents", "Main [default]", "Scout [review]", "Select an agent to watch.", "thread-worker"} {
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

func TestModelAgentPickerShowsCacheImmediatelyAndRefreshesInBackground(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	reads := 0
	model := NewModel(state, Options{
		OnReadAgents: func(currentThreadID string) ([]codextui.AgentThreadEntry, error) {
			reads++
			return []codextui.AgentThreadEntry{
				{ThreadID: "thread-main", IsPrimary: true},
				{ThreadID: "thread-worker", AgentNickname: "Scout", IsRunning: false},
				{ThreadID: "thread-new", AgentNickname: "Builder", IsRunning: true},
			}, nil
		},
	})
	model.agentItems = []codextui.AgentThreadEntry{
		{ThreadID: "thread-main", IsPrimary: true},
		{ThreadID: "thread-worker", AgentNickname: "Scout", IsRunning: true},
	}

	cmd := model.applyAgentCommand()
	if cmd == nil || model.modal == nil || model.modal.id != "agent-picker" {
		t.Fatalf("cached picker was not opened immediately: modal=%#v cmd=%v", model.modal, cmd != nil)
	}
	if strings.Contains(model.View(), "Loading agent threads") || !strings.Contains(model.View(), "Scout") {
		t.Fatalf("cached picker view =\n%s", model.View())
	}
	model.modal.selected = 1
	if coalesced := model.applyAgentCommand(); coalesced != nil {
		t.Fatal("second picker open started a duplicate refresh")
	}
	model.Update(cmd())
	if reads != 1 || model.modal == nil || len(model.modal.options) != 3 {
		t.Fatalf("background refresh reads=%d modal=%#v", reads, model.modal)
	}
	if selected := model.modal.options[model.modal.selected].ID; selected != "thread-worker" {
		t.Fatalf("selection after refresh = %q, want thread-worker", selected)
	}
	if !strings.Contains(model.View(), "Builder") {
		t.Fatalf("refreshed picker missing new agent:\n%s", model.View())
	}
}

func TestModelAgentPickerRejectsRefreshFromClearedSession(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-old")
	model := NewModel(state, Options{
		OnReadAgents: func(currentThreadID string) ([]codextui.AgentThreadEntry, error) {
			return []codextui.AgentThreadEntry{{ThreadID: "thread-old", IsPrimary: true}, {ThreadID: "thread-worker"}}, nil
		},
	})
	cmd := model.applyAgentCommand()
	if cmd == nil {
		t.Fatal("agent picker refresh command missing")
	}
	model.startFreshNamedSession("", "new")
	model.Update(cmd())
	if len(model.agentItems) != 0 || state.ThreadID != "" {
		t.Fatalf("stale refresh repopulated cleared session: items=%#v thread=%q", model.agentItems, state.ThreadID)
	}
}

func TestModelAgentSwitchReplaysBufferedBackgroundEvents(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	model := NewModel(state, Options{
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{
				Entry:    codextui.AgentThreadEntry{ThreadID: threadID, AgentNickname: "Scout", AgentRole: "worker"},
				Messages: nil, // running agent: nothing persisted yet
				Status:   "running",
			}, nil
		},
	})
	// Background events for the running subagent arrive while it is not the
	// active thread and must be buffered instead of dropped.
	model.Update(ThreadScopedEventMsg{ThreadID: "thread-worker", Event: protocol.ThreadEvent{Type: "item.completed", Item: &protocol.ThreadItem{ID: "user-1", Type: "user_message", Text: "worker prompt"}}})
	model.Update(ThreadScopedEventMsg{ThreadID: "thread-worker", Event: protocol.ThreadEvent{Type: "item.delta", Delta: &protocol.Delta{ItemID: "agent-1", Text: "work"}}})
	model.Update(ThreadScopedEventMsg{ThreadID: "thread-worker", Event: protocol.ThreadEvent{Type: "item.completed", Item: &protocol.ThreadItem{ID: "agent-1", Type: "agent_message", Text: "working..."}}})
	model.Update(ThreadScopedEventMsg{ThreadID: "thread-worker", Event: protocol.ThreadEvent{Type: "item.completed", Item: &protocol.ThreadItem{ID: "cmd-1", Type: "command_execution", Command: "go test"}}})
	if len(model.backgroundThreadEvents["thread-worker"]) != 4 {
		t.Fatalf("buffered events = %d, want 4", len(model.backgroundThreadEvents["thread-worker"]))
	}
	// Switching to the agent replays its in-progress activity.
	model.Update(AgentSwitchResultMsg{
		ThreadID: "thread-worker",
		Response: AgentThreadSwitchResponse{
			Entry:    codextui.AgentThreadEntry{ThreadID: "thread-worker", AgentNickname: "Scout", AgentRole: "worker"},
			Status:   "running",
			Messages: nil,
		},
	})
	if state.ThreadID != "thread-worker" {
		t.Fatalf("thread = %q, want thread-worker", state.ThreadID)
	}
	joined := ""
	for _, message := range state.Messages {
		joined += message.Text + "\n"
	}
	for _, want := range []string{"worker prompt", "working...", "$ go test"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("replayed transcript missing %q:\n%s", want, joined)
		}
	}
	if len(model.backgroundThreadEvents["thread-worker"]) != 0 {
		t.Fatalf("buffer not cleared after replay")
	}
}

func TestModelAgentFastNavigationLoadsAndWraps(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	var switched []string
	model := NewModel(state, Options{
		OnReadAgents: func(currentThreadID string) ([]codextui.AgentThreadEntry, error) {
			return []codextui.AgentThreadEntry{
				{ThreadID: "thread-main", IsPrimary: true},
				{ThreadID: "thread-worker", AgentNickname: "Scout"},
			}, nil
		},
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			switched = append(switched, threadID)
			return AgentThreadSwitchResponse{Entry: codextui.AgentThreadEntry{ThreadID: threadID}, Status: "idle"}, nil
		},
	})

	_, loadCmd := model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRight, Alt: true}))
	if loadCmd == nil {
		t.Fatal("alt+right did not load agent threads")
	}
	_, switchCmd := model.Update(loadCmd())
	if switchCmd == nil {
		t.Fatal("loaded navigation did not switch agents")
	}
	model.Update(switchCmd())
	if len(switched) != 1 || switched[0] != "thread-worker" || state.ThreadID != "thread-worker" {
		t.Fatalf("first navigation switched=%#v thread=%q", switched, state.ThreadID)
	}

	_, switchCmd = model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRight, Alt: true}))
	if switchCmd == nil {
		t.Fatal("cached alt+right did not switch agents")
	}
	model.Update(switchCmd())
	if len(switched) != 2 || switched[1] != "thread-main" || state.ThreadID != "thread-main" {
		t.Fatalf("wrapped navigation switched=%#v thread=%q", switched, state.ThreadID)
	}

	typeText(t, model, "draft")
	_, cmd := model.Update(bubbletea.KeyMsg(bubbletea.Key{Type: bubbletea.KeyRight, Alt: true}))
	if cmd != nil || len(switched) != 2 {
		t.Fatalf("non-empty draft triggered navigation cmd=%#v switched=%#v", cmd, switched)
	}
}

func TestModelMentionCommandOpensUnifiedMentionPopup(t *testing.T) {
	queries := []string{}
	model := NewModel(codextui.NewState(nil), Options{
		FeatureSettings: map[string]bool{"mentions_v2": true},
		OnFuzzyFileSearch: func(query string, cwd string, cancellationToken string) (appserver.FuzzyFileSearchResponse, error) {
			queries = append(queries, query)
			return appserver.FuzzyFileSearchResponse{}, nil
		},
	})
	typeText(t, model, "/mention")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.ComposerValue() != "@" || model.mentionPopup == nil {
		t.Fatalf("mention command composer=%q popup=%#v", model.ComposerValue(), model.mentionPopup)
	}
	if !reflect.DeepEqual(queries, []string{""}) {
		t.Fatalf("mention queries = %#v", queries)
	}
}

func TestModelTestApprovalMatchesPatchApprovalFixture(t *testing.T) {
	var responses []ModalResponse
	model := NewModel(codextui.NewState(nil), Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	typeText(t, model, "/test-approval")
	model.Update(key(bubbletea.KeyEnter))
	view := model.View()
	for _, want := range []string{"Would you like to make the following edits?", "Yes, proceed", "don't ask again for these files", "No, and tell Codex what to do differently"} {
		if !strings.Contains(view, want) {
			t.Fatalf("test approval missing %q:\n%s", want, view)
		}
	}
	for _, unexpected := range []string{"Grant root", "/tmp/test.txt", "/tmp/test2.txt"} {
		if strings.Contains(view, unexpected) {
			t.Fatalf("test approval unexpectedly contains %q:\n%s", unexpected, view)
		}
	}
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(responses) != 1 || responses[0].ID != "1" || responses[0].Kind != ModalKindApproval || responses[0].OptionID != "accept" || responses[0].Cancelled {
		t.Fatalf("test approval response = %#v", responses)
	}
}

func TestModelMisalignmentPolicyViolationStopsChat(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 200, Height: 36})
	model.queueComposer(true)
	model.composer.SetValue("draft")
	updated, cmd := model.Update(TurnCompletedMsg{Err: errors.New(`{"error":{"code":"misalignment_policy_violation","message":"blocked"}}`)})
	if cmd != nil {
		t.Fatalf("misalignment stop cmd = %#v, want nil", cmd)
	}
	stopped := updated.(*Model)
	if !stopped.misalignmentPolicyStopped {
		t.Fatal("misalignment policy stop not recorded")
	}
	if len(stopped.queued) != 0 || len(stopped.rejectedSteers) != 0 {
		t.Fatalf("queued input should be cleared: queued=%#v rejected=%#v", stopped.queued, stopped.rejectedSteers)
	}
	if got := stopped.composer.Value(); got != "" {
		t.Fatalf("composer should be cleared, got %q", got)
	}
	stopped.composer.SetValue("new")
	next, cmd := stopped.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if cmd != nil {
		t.Fatalf("stopped chat submit cmd = %#v, want nil (submit rejected)", cmd)
	}
	if got := next.(*Model).SubmittedRequests(); len(got) != 0 {
		t.Fatalf("stopped chat should not submit requests, got %#v", got)
	}
	if got := next.(*Model).State.Status; strings.EqualFold(strings.TrimSpace(got), "running") {
		t.Fatalf("stopped chat should not start a turn, status = %q", got)
	}
}

func TestModelAsyncAgentMessageRendersWithoutEndingTurn(t *testing.T) {
	// Rust #39312: an agent message with delivery=async is user-visible but
	// does not become the turn's final answer.
	model := NewModel(codextui.NewState(nil), Options{Width: 200, Height: 36})
	model.State.SetThreadID("thread-async")
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{
		Type: "item.completed",
		Item: &protocol.ThreadItem{ID: "async-1", Type: "agent_message", Text: "still working", Delivery: "async"},
	}})
	view := model.View()
	if !strings.Contains(view, "still working") {
		t.Fatalf("async message not rendered:\n%s", view)
	}
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{
		Type: "item.completed",
		Item: &protocol.ThreadItem{ID: "final-1", Type: "agent_message", Text: "done"},
	}})
	view = model.View()
	if !strings.Contains(view, "done") {
		t.Fatalf("final message not rendered:\n%s", view)
	}
}

func TestModelForkCommandForksCurrentSessionImmediately(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	now := fixedTeaTime()
	var responses []ModalResponse
	var actions []codextui.SessionSelection
	model := NewModel(state, Options{
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
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(actions) != 1 || actions[0].Kind != codextui.SessionSelectionFork || actions[0].Target.ThreadID != "thread-source" {
		t.Fatalf("actions = %#v", actions)
	}
	if state.ThreadID != "thread-forked" {
		t.Fatalf("ThreadID = %q, want thread-forked", state.ThreadID)
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != string(codextui.SessionSelectionFork) || responses[0].Picker.Value != "thread-forked" {
		t.Fatalf("responses = %#v", responses)
	}
	if model.modal != nil {
		t.Fatalf("fork opened modal = %#v, want immediate action", model.modal)
	}
}

func TestModelForkCommandNamesForkAndKeepsItOpenOnRenameFailure(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	var actions []codextui.SessionSelection
	var renameName string
	model := NewModel(state, Options{
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			actions = append(actions, selection)
			return &codextui.SessionSummary{ThreadID: "thread-forked", Title: "Old"}, nil
		},
		OnRenameThread: func(_ string, name string) error {
			renameName = name
			return errors.New("rename failed")
		},
	})

	typeText(t, model, "/fork Add User")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(actions) != 1 || actions[0].Name != "Add User" || renameName != "Add User" {
		t.Fatalf("named fork actions = %#v rename=%q", actions, renameName)
	}
	if state.ThreadID != "thread-forked" {
		t.Fatalf("rename failure did not keep fork open: %q", state.ThreadID)
	}
	if view := model.View(); !strings.Contains(view, "Failed to name the forked session: rename failed") {
		t.Fatalf("rename failure missing from history:\n%s", view)
	}
}

func TestModelDeleteCommandConfirmsCurrentSessionAndExits(t *testing.T) {
	now := fixedTeaTime()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-delete")
	var responses []ModalResponse
	var actions []codextui.SessionSelection
	model := NewModel(state, Options{
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
	if view := model.View(); !strings.Contains(view, "Delete this session?") || !strings.Contains(view, "Cannot be undone. Subagent threads will also be deleted.") || !strings.Contains(view, "No, keep this session") {
		t.Fatalf("delete confirmation view:\n%s", view)
	}
	model.Update(key(bubbletea.KeyDown))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if len(actions) != 1 || actions[0].Kind != codextui.SessionSelectionDelete || actions[0].Target.ThreadID != "thread-delete" {
		t.Fatalf("actions = %#v", actions)
	}
	if len(model.sessionItems) != 0 {
		t.Fatalf("sessionItems = %#v, want empty after delete", model.sessionItems)
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != string(codextui.SessionSelectionDelete) || responses[0].Picker.Value != "thread-delete" {
		t.Fatalf("responses = %#v", responses)
	}
	if state.ThreadID != "" {
		t.Fatalf("ThreadID = %q, want reset after delete", state.ThreadID)
	}
	if !batchContainsMessageType(cmd, bubbletea.QuitMsg{}) {
		t.Fatal("delete confirmation did not request exit")
	}
}

func TestModelArchiveCommandConfirmsCurrentSessionAndExits(t *testing.T) {
	now := fixedTeaTime()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-archive")
	var responses []ModalResponse
	var actions []codextui.SessionSelection
	model := NewModel(state, Options{
		SessionPickerItems: []codextui.SessionSummary{{
			ThreadID:  "thread-archive",
			Title:     "Archive Me",
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

	typeText(t, model, "/archive")
	model.Update(key(bubbletea.KeyEnter))
	if view := model.View(); !strings.Contains(view, "Archive this session?") || !strings.Contains(view, "This will archive the current session and exit Codex") || !strings.Contains(view, "No, keep this session") {
		t.Fatalf("archive confirmation view:\n%s", view)
	}
	model.Update(key(bubbletea.KeyDown))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if len(actions) != 1 || actions[0].Kind != codextui.SessionSelectionArchive || actions[0].Target.ThreadID != "thread-archive" {
		t.Fatalf("actions = %#v", actions)
	}
	if len(responses) != 1 || responses[0].Picker == nil || responses[0].Picker.Kind != string(codextui.SessionSelectionArchive) || responses[0].Picker.Value != "thread-archive" {
		t.Fatalf("responses = %#v", responses)
	}
	if len(model.sessionItems) != 1 || !model.sessionItems[0].Archived {
		t.Fatalf("sessionItems = %#v, want archived current session", model.sessionItems)
	}
	if !batchContainsMessageType(cmd, bubbletea.QuitMsg{}) {
		t.Fatal("archive confirmation did not request exit")
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

func TestModelRequestUserInputEnterOnOtherOpensNotesWithoutSubmittingLikeRust(t *testing.T) {
	var responses []ModalResponse
	model := NewModel(nil, Options{
		OnModalResponse: func(response ModalResponse) bubbletea.Cmd {
			responses = append(responses, response)
			return nil
		},
	})
	model.Update(RequestUserInputMsg{
		ID: "user-input-other-enter",
		Questions: []codextui.RequestUserInputQuestion{{
			ID:       "scope",
			Question: "Where should this apply?",
			IsOther:  true,
			Options:  []codextui.RequestUserInputChoice{{Label: "Plan"}, {Label: "All"}},
		}},
	})
	// Select the generated "Other" option (last) and press Enter: Rust #38624
	// opens the notes editor instead of submitting the response.
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyDown))
	model.Update(key(bubbletea.KeyEnter))
	if len(responses) != 0 {
		t.Fatalf("responses after Enter on Other = %#v, want none until notes submitted", responses)
	}
	if !strings.Contains(model.View(), "Notes:") && !strings.Contains(model.View(), "Add notes") {
		t.Fatalf("Enter on Other should reveal the notes editor:\n%s", model.View())
	}

	typeText(t, model, "custom via enter")
	model.Update(key(bubbletea.KeyEnter))
	if len(responses) != 1 || responses[0].UserInput == nil {
		t.Fatalf("responses = %#v", responses)
	}
	if got := responses[0].UserInput.AnswerLists["scope"]; len(got) != 2 || got[0] != codextui.RequestUserInputOtherOptionLabel || got[1] != "user_note: custom via enter" {
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
