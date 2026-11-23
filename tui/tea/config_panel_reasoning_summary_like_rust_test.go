package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// TestConfigPanelReasoningSummaryLikeRust covers Rust #62905: the Sessions
// group gains a reasoning summary row that shows the connected app server's
// effective value and saves an explicit choice (including Auto).
func TestConfigPanelReasoningSummaryLikeRust(t *testing.T) {
	row := configPanelRowByLabel(t, "Reasoning summary")
	if row.section != configSectionSessions {
		t.Fatalf("reasoning summary section = %v, want Sessions", row.section)
	}

	// A server that never reported a value keeps the row unavailable.
	unknown := configPanelModel(t, nil)
	if got := unknown.configPanelValue(row); got != "Unavailable" {
		t.Fatalf("unavailable value = %q", got)
	}
	unknown.openConfigPanel()
	if cmd := unknown.activateConfigRow(row); cmd != nil {
		t.Fatal("unavailable row produced a command")
	}
	if !strings.Contains(unknown.notice, "Reasoning summary is unavailable.") {
		t.Fatalf("unavailable notice = %q", unknown.notice)
	}

	var saved []SettingsEdit
	state := codextui.NewState(nil)
	state.ReasoningSummaryAvailable = true
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			saved = append(saved, edits...)
			return SettingsWriteResult{ReasoningSummary: "auto", ReasoningSummaryAvailable: true}, nil
		},
	})
	if got := model.configPanelValue(row); got != "Auto (default) \u203a" {
		t.Fatalf("unset value = %q, want Auto (default)", got)
	}

	model.openConfigPanel()
	model.modal.configPanel.section = configSectionSessions
	if cmd := model.activateConfigRow(row); cmd != nil {
		t.Fatal("opening the picker produced a command")
	}
	choice := model.modal.configPanel.choice
	if choice == nil {
		t.Fatal("reasoning summary row did not open a picker")
	}
	wantLabels := []string{"Auto (default)", "Concise", "Detailed", "None"}
	if len(choice.items) != len(wantLabels) {
		t.Fatalf("picker items = %#v", choice.items)
	}
	for index, want := range wantLabels {
		if choice.items[index].label != want {
			t.Fatalf("picker item %d = %q, want %q", index, choice.items[index].label, want)
		}
	}
	if choice.items[choice.selected].value != "auto" {
		t.Fatalf("initial selection = %#v, want Auto", choice.items[choice.selected])
	}

	cmd := model.applyConfigPanelChoiceItem(configActionReasoningSummary, configPanelChoiceItem{label: "Auto", value: "auto"})
	if cmd == nil {
		t.Fatal("saving the reasoning summary produced no command")
	}
	cmd()
	if len(saved) != 1 || saved[0].KeyPath != "model_reasoning_summary" || saved[0].Value != "auto" {
		t.Fatalf("saved edits = %#v, want model_reasoning_summary=auto", saved)
	}
	if state.ReasoningSummary != "auto" {
		t.Fatalf("live summary = %q, want auto", state.ReasoningSummary)
	}
}

// TestConfigPanelReasoningSummarySaveNoticesLikeRust pins the two save
// notices Rust #62905 reports: the choice applied, or a higher-priority config
// or launch setting won.
func TestConfigPanelReasoningSummarySaveNoticesLikeRust(t *testing.T) {
	model := configPanelModel(t, nil)
	model.openConfigPanel()
	model.pendingConfigPanelSave = &configPanelPendingSave{keyPath: "model_reasoning_summary", value: "concise"}
	if notice, ok := model.finishConfigPanelSave(SettingsWriteResult{ReasoningSummary: "concise", ReasoningSummaryAvailable: true}); !ok || notice != "Saved \u00b7 applies to new chats" {
		t.Fatalf("matching notice = %q, %v", notice, ok)
	}
	model.pendingConfigPanelSave = &configPanelPendingSave{keyPath: "model_reasoning_summary", value: "concise"}
	if notice, ok := model.finishConfigPanelSave(SettingsWriteResult{ReasoningSummary: "detailed", ReasoningSummaryAvailable: true}); !ok || notice != "Saved \u00b7 overridden by config or launch setting" {
		t.Fatalf("overridden notice = %q, %v", notice, ok)
	}
}
