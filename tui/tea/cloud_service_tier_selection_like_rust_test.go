package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

func TestCloudServiceTierSelectionStaysConversationScopedLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Model = "gpt-5"
	writes := 0
	model := NewModel(state, Options{
		ServerOwnedSettings: true,
		OnWriteSettings: func([]SettingsEdit) (SettingsWriteResult, error) {
			writes++
			return SettingsWriteResult{FilePath: "config.toml"}, nil
		},
	})
	command := bottompane.ServiceTierCommand{ID: "priority", Name: "fast"}
	if cmd := model.applyServiceTierCommand(command); cmd != nil {
		t.Fatal("Cloud speed selection started a settings write")
	}
	if state.ServiceTier != "priority" {
		t.Fatalf("selected tier = %q, want priority", state.ServiceTier)
	}
	if writes != 0 {
		t.Fatalf("settings writes = %d, want none", writes)
	}
	if !strings.Contains(model.notice, "Selected fast speed for the next new turn in this conversation") {
		t.Fatalf("selection notice = %q", model.notice)
	}

	if cmd := model.applyServiceTierCommand(command); cmd != nil {
		t.Fatal("clearing Cloud speed started a settings write")
	}
	if state.ServiceTier != "default" {
		t.Fatalf("cleared tier = %q, want default", state.ServiceTier)
	}
	if !strings.Contains(model.notice, "Selected standard speed for the next new turn") {
		t.Fatalf("clear notice = %q", model.notice)
	}
	if writes != 0 {
		t.Fatalf("settings writes after clear = %d, want none", writes)
	}
}

func TestClientOwnedServiceTierSelectionStillWritesSettings(t *testing.T) {
	state := codextui.NewState(nil)
	state.Model = "gpt-5"
	model := NewModel(state, Options{
		OnWriteSettings: func([]SettingsEdit) (SettingsWriteResult, error) {
			return SettingsWriteResult{FilePath: "config.toml"}, nil
		},
	})
	command := bottompane.ServiceTierCommand{ID: "priority", Name: "fast"}
	if cmd := model.applyServiceTierCommand(command); cmd == nil {
		t.Fatal("client-owned speed selection did not request a settings write")
	}
	if state.ServiceTier != "priority" {
		t.Fatalf("selected tier = %q, want priority", state.ServiceTier)
	}
}
