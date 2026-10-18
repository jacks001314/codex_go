package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
	"codex_go/utils"
)

func TestModelSafetyBufferingShowsRetryPromptOnce(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-safety")
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 120, Height: 40})
	model.currentTurnID = "turn-safety"
	faster := "gpt-fast"
	model.Update(ModelSafetyBufferingMsg{
		ThreadID:        "thread-safety",
		TurnID:          "turn-safety",
		Model:           "gpt-safe",
		ShowBufferingUI: true,
		FasterModel:     &faster,
	})
	if model.modal == nil || model.modal.id != chatwidget.SafetyBufferingPromptViewID {
		t.Fatalf("buffering prompt modal = %#v", model.modal)
	}
	if model.modal.title != chatwidget.SafetyBufferingHeader {
		t.Fatalf("prompt title = %q", model.modal.title)
	}

	// A second update for the same turn refreshes state without opening a
	// second prompt.
	model.Update(ModelSafetyBufferingMsg{
		ThreadID:        "thread-safety",
		TurnID:          "turn-safety",
		ShowBufferingUI: true,
		FasterModel:     &faster,
	})
	if model.modal == nil || model.modal.id != chatwidget.SafetyBufferingPromptViewID {
		t.Fatalf("second update replaced the prompt: %#v", model.modal)
	}
}

func TestModelSafetyBufferingRetryConfirmationAndStop(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-safety")
	state.SetStatus("running")
	state.AddMessage(codextui.RoleUser, "retry me please")
	var retried []string
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		ModelPickerOptions: []codextui.ModelPickerOption{
			{ID: "gpt-fast", Label: "Faster Model"},
		},
		OnSafetyBufferingRetry: func(threadID, turnID, model, prompt string) bubbletea.Cmd {
			retried = append(retried, threadID+"|"+turnID+"|"+model+"|"+prompt)
			return nil
		},
	})
	model.currentTurnID = "turn-safety"
	faster := "gpt-fast"
	model.Update(ModelSafetyBufferingMsg{
		ThreadID:        "thread-safety",
		TurnID:          "turn-safety",
		ShowBufferingUI: true,
		FasterModel:     &faster,
	})
	model.applySafetyBufferingModalOption("retry")
	if model.modal == nil || !strings.Contains(model.modal.title, "Stop this attempt and retry?") {
		t.Fatalf("confirmation modal = %#v", model.modal)
	}
	if !strings.Contains(model.modal.body, "using Faster Model") || strings.Contains(model.modal.body, "gpt-fast") {
		t.Fatalf("confirmation body = %q", model.modal.body)
	}

	model.applySafetyBufferingModalOption("stop-retry")
	if len(retried) != 1 || retried[0] != "thread-safety|turn-safety|gpt-fast|retry me please" {
		t.Fatalf("retry callbacks = %#v", retried)
	}
}

func TestModelSafetyBufferingKeepWaitingRetainsRunningStateLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-safety")
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 100, Height: 30})
	model.currentTurnID = "turn-safety"
	faster := "gpt-fast"
	model.Update(ModelSafetyBufferingMsg{
		ThreadID:        "thread-safety",
		TurnID:          "turn-safety",
		ShowBufferingUI: true,
		FasterModel:     &faster,
	})
	model.applySafetyBufferingModalOption("retry")
	model.applySafetyBufferingModalOption("wait")

	if model.modal != nil {
		t.Fatalf("keep waiting left modal open: %#v", model.modal)
	}
	if !model.safetyBuffering.IsWaiting() || model.safetyBuffering.ActiveTurnID != "turn-safety" {
		t.Fatalf("keep waiting cleared the active buffering state: %#v", model.safetyBuffering)
	}
}

func TestModelSafetyBufferingResponseStartDismissesAndBlocksStalePromptLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-safety")
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 80, Height: 30})
	model.currentTurnID = "turn-safety"
	faster := "gpt-fast"
	model.Update(ModelSafetyBufferingMsg{
		ThreadID:        "thread-safety",
		TurnID:          "turn-safety",
		ShowBufferingUI: true,
		FasterModel:     &faster,
	})
	if model.modal == nil {
		t.Fatal("safety buffering prompt was not opened")
	}

	model.appendAssistantDelta("answer", "Visible response")
	if model.modal != nil || model.safetyBuffering.IsWaiting() {
		t.Fatalf("response start left buffering UI active: modal=%#v state=%#v", model.modal, model.safetyBuffering)
	}

	model.Update(ModelSafetyBufferingMsg{
		ThreadID:        "thread-safety",
		TurnID:          "turn-safety",
		ShowBufferingUI: true,
		FasterModel:     &faster,
	})
	if model.modal != nil {
		t.Fatalf("stale buffering update reopened prompt: %#v", model.modal)
	}
}

func TestModelSafetyBufferingLearnMoreOpensHelpWithoutRetry(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-safety")
	state.SetStatus("running")
	var opened []string
	model := NewModel(state, Options{
		Width:  80,
		Height: 30,
		OnAppLinkAction: func(action AppLinkAction) bubbletea.Cmd {
			opened = append(opened, action.URL)
			return nil
		},
	})
	model.currentTurnID = "turn-safety"
	faster := "gpt-fast"
	model.Update(ModelSafetyBufferingMsg{
		ThreadID:        "thread-safety",
		TurnID:          "turn-safety",
		ShowBufferingUI: true,
		FasterModel:     &faster,
	})

	model.applySafetyBufferingModalOption("learn-more")
	if len(opened) != 1 || opened[0] != chatwidget.SafetyBufferingLearnMoreURL {
		t.Fatalf("opened URLs = %#v", opened)
	}
}

func TestModelSafetyBufferingConfirmationWrapsNarrowLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-safety")
	state.SetStatus("running")
	model := NewModel(state, Options{
		Width:              40,
		Height:             30,
		ModelPickerOptions: []codextui.ModelPickerOption{{ID: "gpt-fast", Label: "Faster Model"}},
	})
	model.currentTurnID = "turn-safety"
	faster := "gpt-fast"
	model.Update(ModelSafetyBufferingMsg{
		ThreadID:        "thread-safety",
		TurnID:          "turn-safety",
		ShowBufferingUI: true,
		FasterModel:     &faster,
	})
	model.applySafetyBufferingModalOption("retry")
	view := utils.StripANSI(model.renderModal())
	if !strings.Contains(view, "Stop this attempt") || !strings.Contains(view, "Faster Model") {
		t.Fatalf("narrow confirmation missing wrapped copy:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if codextui.DisplayWidth(line) > 40 {
			t.Fatalf("narrow confirmation line width > 40: %q", line)
		}
	}
}

func TestModelSafetyBufferingClearDismissesModal(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-safety")
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 120, Height: 40})
	model.currentTurnID = "turn-safety"
	faster := "gpt-fast"
	model.Update(ModelSafetyBufferingMsg{
		ThreadID:        "thread-safety",
		TurnID:          "turn-safety",
		ShowBufferingUI: true,
		FasterModel:     &faster,
	})
	model.Update(ModelSafetyBufferingMsg{ThreadID: "thread-safety", TurnID: "turn-safety", ShowBufferingUI: false})
	if model.modal != nil || model.safetyBuffering.ActiveTurnID != "" {
		t.Fatalf("clear left buffering state: modal=%v state=%#v", model.modal, model.safetyBuffering)
	}
}
