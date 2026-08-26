package chatwidget

import (
	"strings"
	"testing"
)

func TestReasoningPopupSingleEffortAppliesImmediatelyMatchRust(t *testing.T) {
	preset := ModelPopupPreset{
		Model:                  "gpt-5",
		DefaultReasoningEffort: "medium",
		SupportedReasoningEfforts: []ReasoningEffortPopupOption{
			{Effort: "medium"},
		},
	}

	result := NewReasoningPopupView(ModelPopupConfig{CurrentModel: "other"}, preset)

	if result.View.ViewID != "" || result.ApplyModel != "gpt-5" || result.ApplyReasoningEffort != "medium" || result.OpenPlanReasoningScope {
		t.Fatalf("single effort result = %#v", result)
	}
}

func TestReasoningPopupSingleEffortOpensPlanScopeWhenNeededMatchRust(t *testing.T) {
	preset := ModelPopupPreset{
		Model:                  "gpt-5",
		DefaultReasoningEffort: "high",
		SupportedReasoningEfforts: []ReasoningEffortPopupOption{
			{Effort: "high"},
		},
	}
	config := ModelPopupConfig{
		CurrentModel:               "gpt-5",
		CurrentReasoningEffort:     "medium",
		CurrentPlanReasoningEffort: "medium",
		PlanMode:                   true,
		CollaborationModesEnabled:  true,
	}

	result := NewReasoningPopupView(config, preset)

	if !result.OpenPlanReasoningScope || result.ApplyModel != "gpt-5" || result.ApplyReasoningEffort != "high" {
		t.Fatalf("plan scope result = %#v", result)
	}
}

func TestReasoningPopupDefaultHighlightAndWarningDescriptionMatchRust(t *testing.T) {
	preset := ModelPopupPreset{
		Model:                  "gpt-5.2",
		DefaultReasoningEffort: "not-supported",
		SupportedReasoningEfforts: []ReasoningEffortPopupOption{
			{Effort: "low", Description: "Low cost."},
			{Effort: "high", Description: "More thorough."},
		},
	}
	config := ModelPopupConfig{CurrentModel: "different", CurrentReasoningEffort: "high"}

	result := NewReasoningPopupView(config, preset)

	if result.View.InitialSelectedIndex != 0 || !result.View.Items[0].IsCurrent || !result.View.Items[0].IsDefault {
		t.Fatalf("default selection = index %d items=%#v", result.View.InitialSelectedIndex, result.View.Items)
	}
	selectedDescription := result.View.Items[1].SelectedDescription
	if !strings.Contains(selectedDescription, "More thorough.") || !strings.Contains(selectedDescription, "Plus plan rate limits") {
		t.Fatalf("selected description = %q", selectedDescription)
	}
}

func TestReasoningEffortPopupLabelPersistent(t *testing.T) {
	if got := ReasoningEffortPopupLabel("persistent"); got != "Persistent" {
		t.Fatalf("label = %q, want Persistent (Rust #40799)", got)
	}
}
