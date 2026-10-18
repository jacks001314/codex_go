package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

func daybreakCommandModel(t *testing.T) (*Model, *[]bool) {
	t.Helper()
	updates := []bool{}
	state := codextui.NewState(&codextui.Options{
		Model:             "gpt-5",
		Provider:          "openai",
		AccountDisplay:    "user@example.com (Plus)",
		HasChatGPTAccount: true,
	})
	state.SetThreadID("thread-daybreak")
	model := NewModel(state, Options{
		Width:             80,
		Height:            24,
		HasChatGPTAccount: true,
		FeatureSettings:   map[string]bool{"cli_daybreak": true},
		ModelPickerOptions: []codextui.ModelPickerOption{{
			ID:                     "gpt-5",
			AvailableCyberPrograms: []string{"standard", "daybreak_blue"},
		}},
		OnUpdateDaybreak: func(_ string, enabled bool) error {
			updates = append(updates, enabled)
			return nil
		},
	})
	return model, &updates
}

// Rust #49859: /daybreak validates eligibility/model support, persists the
// thread preference, and does not start a turn.
func TestDaybreakCommandTogglesAndPersistsLikeRust(t *testing.T) {
	model, updates := daybreakCommandModel(t)
	invocation, ok := codextui.ParseCommand("/daybreak")
	if !ok || invocation.Command != codextui.CommandDaybreak {
		t.Fatalf("ParseCommand(/daybreak) = %#v ok=%v", invocation, ok)
	}
	model.applyCommand(invocation)
	if !model.daybreakEnabled || len(*updates) != 1 || !(*updates)[0] {
		t.Fatalf("daybreak=%v updates=%v", model.daybreakEnabled, *updates)
	}
	if !strings.Contains(utils.StripANSI(model.View()), "Daybreak is on for new turns") {
		t.Fatalf("missing enable notice:\n%s", utils.StripANSI(model.View()))
	}
	model.applyCommand(invocation)
	if model.daybreakEnabled || len(*updates) != 2 || (*updates)[1] {
		t.Fatalf("daybreak=%v updates=%v", model.daybreakEnabled, *updates)
	}

	disabled, disabledUpdates := daybreakCommandModel(t)
	disabled.featureSettings["cli_daybreak"] = false
	disabled.applyCommand(invocation)
	if disabled.daybreakEnabled || len(*disabledUpdates) != 0 {
		t.Fatalf("disabled daybreak = %v updates=%v", disabled.daybreakEnabled, *disabledUpdates)
	}
}

// Rust #49859: a refusal offers Enable Daybreak for the next request, persists
// the choice, and never retries the refused turn.
func TestCyberRefusalDaybreakPickerLikeRust(t *testing.T) {
	model, updates := daybreakCommandModel(t)
	model.Update(CyberPolicyErrorMsg{ThreadID: "thread-daybreak"})
	if model.modal == nil || model.modal.id != chatwidget.DaybreakPromptViewID {
		t.Fatalf("modal = %#v, want Daybreak prompt", model.modal)
	}
	model.modal.selected = 0
	model.respondModal(false)
	if !model.daybreakEnabled || len(*updates) != 1 || !(*updates)[0] {
		t.Fatalf("daybreak=%v updates=%v", model.daybreakEnabled, *updates)
	}
	if len(model.submitted) != 0 || len(model.submitRequests) != 0 {
		t.Fatal("refusal picker retried the refused request")
	}

	on, _ := daybreakCommandModel(t)
	on.daybreakEnabled = true
	on.Update(CyberPolicyErrorMsg{ThreadID: "thread-daybreak"})
	if on.modal != nil && on.modal.id == chatwidget.DaybreakPromptViewID {
		t.Fatal("picker shown with Daybreak already on")
	}
}
