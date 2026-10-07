package chatwidget

import (
	"strings"
	"testing"
)

// Rust #51235 popups_and_settings.rs `model_selection_popup_snapshot` (plus
// status_and_layout.rs `workspace_owner_nudge_default_no_dismisses_without_sending`):
// "Remove default model labels from TUI model pickers" dropped
// SelectionItem::is_default, so rows mark the current model only and the
// workspace-owner prompt labels its own negative row "No (default)".
func TestModelSelectionRowsHaveNoDefaultMarkerLikeRust(t *testing.T) {
	presets := []ModelPopupPreset{
		{Model: "gpt-6.1-sol", DisplayName: "GPT-6.1-Sol", Description: "workhorse", ShowInPicker: true, IsDefault: true},
		{Model: "gpt-5.5", DisplayName: "GPT-5.5", Description: "legacy", ShowInPicker: true},
	}
	view := NewModelPopupView(ModelPopupConfig{CurrentModel: "gpt-5.5"}, presets).View
	if view.ViewID != AllModelsSelectionViewID {
		t.Fatalf("view = %q", view.ViewID)
	}
	rows := strings.Join(SelectionViewRows(view, -1, 100), "\n")
	if !strings.Contains(rows, "Currently selected") {
		t.Fatalf("current marker lost:\n%s", rows)
	}
	for _, stale := range []string{"(default)", " Default"} {
		if strings.Contains(rows, stale) {
			t.Fatalf("model rows still mark %q:\n%s", stale, rows)
		}
	}

	// Rust keeps the reasoning effort's own "(default)" label.
	reasoning := NewReasoningPopupView(ModelPopupConfig{CurrentModel: "gpt-5.2"}, ModelPopupPreset{
		Model:                     "gpt-5.2",
		SupportedReasoningEfforts: []ReasoningEffortPopupOption{{Effort: "low"}, {Effort: "high"}},
	}).View
	effortRows := strings.Join(SelectionViewRows(reasoning, -1, 100), "\n")
	if !strings.Contains(effortRows, "Low (default)") {
		t.Fatalf("effort default label lost:\n%s", effortRows)
	}
}

// Rust #51235 rate_limits.rs `open_workspace_owner_nudge_prompt`: the negative
// option carries the default label itself.
func TestWorkspaceOwnerNudgeLabelsItsDefaultRowLikeRust(t *testing.T) {
	rows := strings.Join(SelectionViewRows(NewWorkspaceOwnerNudgePromptView(AddCreditsNudgeCredits), -1, 100), "\n")
	if !strings.Contains(rows, "No (default)") {
		t.Fatalf("nudge rows = %s", rows)
	}
	if strings.Contains(rows, "(default) (default)") {
		t.Fatalf("nudge rows double-labelled: %s", rows)
	}
}
