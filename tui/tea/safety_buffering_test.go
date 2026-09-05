package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

func TestModelSafetyBufferingShowsRetryPromptOnce(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-safety")
	model := NewModel(state, Options{Width: 120, Height: 40})
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
	if !strings.Contains(model.modal.title, "Additional safety checks") {
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
	state.AddMessage(codextui.RoleUser, "retry me please")
	var retried []string
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		OnSafetyBufferingRetry: func(threadID, turnID, model, prompt string) bubbletea.Cmd {
			retried = append(retried, threadID+"|"+turnID+"|"+model+"|"+prompt)
			return nil
		},
	})
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

	model.applySafetyBufferingModalOption("wait")
	if model.modal != nil || model.safetyBuffering.ActiveTurnID != "" {
		t.Fatalf("wait did not clear buffering: modal=%v state=%#v", model.modal, model.safetyBuffering)
	}

	model.Update(ModelSafetyBufferingMsg{
		ThreadID:        "thread-safety",
		TurnID:          "turn-safety",
		ShowBufferingUI: true,
		FasterModel:     &faster,
	})
	model.applySafetyBufferingModalOption("retry")
	model.applySafetyBufferingModalOption("stop-retry")
	if len(retried) != 1 || retried[0] != "thread-safety|turn-safety|gpt-fast|retry me please" {
		t.Fatalf("retry callbacks = %#v", retried)
	}
}

func TestModelSafetyBufferingClearDismissesModal(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-safety")
	model := NewModel(state, Options{Width: 120, Height: 40})
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
