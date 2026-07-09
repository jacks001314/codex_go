package chatwidget

import "testing"

func TestCollaborationModeKindMatchesRustVisibilityAndLabels(t *testing.T) {
	if CollaborationModeKindPlan.DisplayName() != "Plan" || !CollaborationModeKindPlan.IsTUIVisible() || !CollaborationModeKindPlan.AllowsRequestUserInput() {
		t.Fatalf("plan mode metadata mismatch")
	}
	if CollaborationModeKindDefault.DisplayName() != "Default" || !CollaborationModeKindDefault.IsTUIVisible() || CollaborationModeKindDefault.AllowsRequestUserInput() {
		t.Fatalf("default mode metadata mismatch")
	}
	if CollaborationModeKindPairProgramming.DisplayName() != "Pair Programming" || CollaborationModeKindPairProgramming.IsTUIVisible() {
		t.Fatalf("pair programming visibility mismatch")
	}
	if CollaborationModeKindExecute.DisplayName() != "Execute" || CollaborationModeKindExecute.IsTUIVisible() {
		t.Fatalf("execute visibility mismatch")
	}
	if NormalizeCollaborationModeKind("code") != CollaborationModeKindDefault || NormalizeCollaborationModeKind("custom") != CollaborationModeKindDefault {
		t.Fatalf("legacy aliases should normalize to default")
	}
}

func TestCollaborationModeApplyMaskCanClearOptionalFields(t *testing.T) {
	mode := NewCollaborationMode(CollaborationModeKindDefault, "gpt-5.2-codex", "high", "stay focused")
	mask := CollaborationModeMask{
		Name:                  "Clear",
		ReasoningEffort:       CollaborationClearValue(),
		DeveloperInstructions: CollaborationClearValue(),
	}

	got := mode.ApplyMask(mask)
	if got.Mode != CollaborationModeKindDefault || got.Settings.Model != "gpt-5.2-codex" {
		t.Fatalf("base mode/model changed: %#v", got)
	}
	if got.Settings.ReasoningEffort != nil || got.Settings.DeveloperInstructions != nil {
		t.Fatalf("optional fields were not cleared: %#v", got.Settings)
	}
}

func TestCollaborationModePresetsAndCyclingMatchRust(t *testing.T) {
	presets := BuiltinCollaborationModePresets()
	if len(presets) != 2 || presets[0].Mode == nil || *presets[0].Mode != CollaborationModeKindPlan || presets[1].Mode == nil || *presets[1].Mode != CollaborationModeKindDefault {
		t.Fatalf("builtin preset order mismatch: %#v", presets)
	}
	if presets[0].ReasoningEffort.Value == nil || *presets[0].ReasoningEffort.Value != CollaborationPlanDefaultReasoningEffort {
		t.Fatalf("plan preset reasoning mismatch: %#v", presets[0])
	}

	defaultMask, ok := DefaultCollaborationMask(presets)
	if !ok || defaultMask.Mode == nil || *defaultMask.Mode != CollaborationModeKindDefault {
		t.Fatalf("default mask = %#v ok=%v", defaultMask, ok)
	}
	next, ok := NextCollaborationMask(presets, &defaultMask)
	if !ok || next.Mode == nil || *next.Mode != CollaborationModeKindPlan {
		t.Fatalf("next after default = %#v ok=%v", next, ok)
	}
	next, ok = NextCollaborationMask(presets, &next)
	if !ok || next.Mode == nil || *next.Mode != CollaborationModeKindDefault {
		t.Fatalf("next after plan = %#v ok=%v", next, ok)
	}
}

func TestInitialCollaborationMaskAppliesModelOverride(t *testing.T) {
	mask, ok := InitialCollaborationMask(nil, " gpt-5.4-mini ")
	if !ok || mask.Mode == nil || *mask.Mode != CollaborationModeKindDefault || mask.Model == nil || *mask.Model != "gpt-5.4-mini" {
		t.Fatalf("initial mask = %#v ok=%v", mask, ok)
	}
}
