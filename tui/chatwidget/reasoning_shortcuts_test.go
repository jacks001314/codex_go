package chatwidget

import (
	"reflect"
	"testing"
)

func TestNextReasoningEffortRaisesLowersAndClampsMatchRust(t *testing.T) {
	choices := []string{"low", "medium", "high", "xhigh"}
	if got, ok := NextReasoningEffort(choices, "medium", ReasoningShortcutRaise); !ok || got != "high" {
		t.Fatalf("raise = %q %v", got, ok)
	}
	if got, ok := NextReasoningEffort(choices[:3], "medium", ReasoningShortcutLower); !ok || got != "low" {
		t.Fatalf("lower = %q %v", got, ok)
	}
	if got, ok := NextReasoningEffort([]string{"low", "high"}, "medium", ReasoningShortcutRaise); ok || got != "" {
		t.Fatalf("unsupported current = %q %v", got, ok)
	}
	if got, ok := NextReasoningEffort(choices[:3], "low", ReasoningShortcutLower); ok || got != "" {
		t.Fatalf("low bound = %q %v", got, ok)
	}
	if got, ok := NextReasoningEffort(choices[:3], "high", ReasoningShortcutRaise); ok || got != "" {
		t.Fatalf("high bound = %q %v", got, ok)
	}
}

func TestNextReasoningEffortUsesAdvertisedCustomOrderMatchRust(t *testing.T) {
	choices := []string{"high", "low", "max"}
	raise, raiseOK := NextReasoningEffort(choices, "high", ReasoningShortcutRaise)
	lower, lowerOK := NextReasoningEffort(choices, "max", ReasoningShortcutLower)
	if !raiseOK || !lowerOK || raise != "low" || lower != "low" {
		t.Fatalf("custom order raise/lower = %q/%q ok=%v/%v", raise, lower, raiseOK, lowerOK)
	}
}

func TestReasoningChoicesAndCurrentAnchorMatchRust(t *testing.T) {
	preset := ReasoningModelPreset{
		Model:                  "gpt-test",
		DefaultReasoningEffort: "medium",
		SupportedReasoningEfforts: []ReasoningEffortOption{
			{Effort: "low"},
			{Effort: "medium"},
			{Effort: "high"},
		},
	}
	if got := ReasoningChoices(preset); !reflect.DeepEqual(got, []string{"low", "medium", "high"}) {
		t.Fatalf("choices = %#v", got)
	}
	if got := ResolveReasoningShortcutCurrentEffort(preset, "unsupported"); got != "medium" {
		t.Fatalf("unsupported configured anchors to default = %q", got)
	}
	preset.DefaultReasoningEffort = "minimal"
	if got := ResolveReasoningShortcutCurrentEffort(preset, "unsupported"); got != "low" {
		t.Fatalf("missing default anchors to first advertised = %q", got)
	}
	preset.SupportedReasoningEfforts = nil
	if got := ReasoningChoices(preset); !reflect.DeepEqual(got, []string{"minimal"}) {
		t.Fatalf("empty supported choices = %#v", got)
	}
}

// Rust #48116: raising reaches Max, Ultra still opens the picker guidance, and
// the footer guidance names the model path (an auto model is named directly).
func TestDecideReasoningShortcutReachesMaxButNotUltraLikeRust(t *testing.T) {
	preset := &ReasoningModelPreset{
		Model:                  "gpt-test",
		DefaultReasoningEffort: "high",
		SupportedReasoningEfforts: []ReasoningEffortOption{
			{Effort: "high"},
			{Effort: "max"},
			{Effort: "ultra"},
		},
	}
	raised := DecideReasoningShortcut(ReasoningShortcutRaise, ReasoningShortcutContext{
		SessionConfigured:         true,
		CurrentModel:              "gpt-test",
		ConfiguredReasoningEffort: "high",
		Preset:                    preset,
	})
	if !raised.Handled || raised.Action != ReasoningShortcutApplyNormal || raised.Effort != "max" {
		t.Fatalf("raise to max = %#v", raised)
	}
	plan := DecideReasoningShortcut(ReasoningShortcutRaise, ReasoningShortcutContext{
		SessionConfigured:         true,
		CollaborationModesEnabled: true,
		PlanModeActive:            true,
		CurrentModel:              "gpt-test",
		ConfiguredReasoningEffort: "high",
		Preset:                    preset,
	})
	if plan.Action != ReasoningShortcutApplyPlanOverride || plan.Effort != "max" {
		t.Fatalf("plan raise to max = %#v", plan)
	}
	ultra := DecideReasoningShortcut(ReasoningShortcutRaise, ReasoningShortcutContext{
		SessionConfigured:         true,
		CurrentModel:              "gpt-test",
		ConfiguredReasoningEffort: "max",
		Preset:                    preset,
	})
	if ultra.Action != ReasoningShortcutInfo || ultra.Effort != "max" ||
		ultra.Info != "Ultra is available under /model → All models → gpt-test → More reasoning…" {
		t.Fatalf("raise to ultra = %#v", ultra)
	}
	auto := DecideReasoningShortcut(ReasoningShortcutRaise, ReasoningShortcutContext{
		SessionConfigured:         true,
		CurrentModel:              "codex-auto-compact",
		ConfiguredReasoningEffort: "max",
		Preset: &ReasoningModelPreset{
			Model:                     "codex-auto-compact",
			DefaultReasoningEffort:    "high",
			SupportedReasoningEfforts: []ReasoningEffortOption{{Effort: "max"}, {Effort: "ultra"}},
		},
	})
	if auto.Info != "Ultra is available under /model → codex-auto-compact → More reasoning…" {
		t.Fatalf("auto model guidance = %#v", auto)
	}
	// Lowering never opens the guidance.
	lowered := DecideReasoningShortcut(ReasoningShortcutLower, ReasoningShortcutContext{
		SessionConfigured:         true,
		CurrentModel:              "gpt-test",
		ConfiguredReasoningEffort: "ultra",
		Preset:                    preset,
	})
	if lowered.Action != ReasoningShortcutApplyNormal || lowered.Effort != "max" {
		t.Fatalf("lower from ultra = %#v", lowered)
	}
}

func TestDecideReasoningShortcutActionsMatchRust(t *testing.T) {
	preset := &ReasoningModelPreset{
		Model:                  "gpt-test",
		DefaultReasoningEffort: "medium",
		SupportedReasoningEfforts: []ReasoningEffortOption{
			{Effort: "low"},
			{Effort: "medium"},
			{Effort: "high"},
		},
	}
	normal := DecideReasoningShortcut(ReasoningShortcutRaise, ReasoningShortcutContext{
		SessionConfigured:         true,
		CurrentModel:              "gpt-test",
		ConfiguredReasoningEffort: "medium",
		Preset:                    preset,
	})
	if !normal.Handled || normal.Action != ReasoningShortcutApplyNormal || normal.Effort != "high" {
		t.Fatalf("normal decision = %#v", normal)
	}
	plan := DecideReasoningShortcut(ReasoningShortcutLower, ReasoningShortcutContext{
		SessionConfigured:         true,
		CollaborationModesEnabled: true,
		PlanModeActive:            true,
		CurrentModel:              "gpt-test",
		ConfiguredReasoningEffort: "medium",
		Preset:                    preset,
	})
	if plan.Action != ReasoningShortcutApplyPlanOverride || plan.Effort != "low" {
		t.Fatalf("plan decision = %#v", plan)
	}
	bound := DecideReasoningShortcut(ReasoningShortcutLower, ReasoningShortcutContext{
		SessionConfigured:         true,
		CurrentModel:              "gpt-test",
		ConfiguredReasoningEffort: "low",
		Preset:                    preset,
	})
	if bound.Action != ReasoningShortcutInfo || bound.Info != "Reasoning is already at the lowest level (low)." {
		t.Fatalf("bound decision = %#v", bound)
	}
	disabled := DecideReasoningShortcut(ReasoningShortcutRaise, ReasoningShortcutContext{})
	if !disabled.Handled || disabled.Info != "Reasoning shortcuts are disabled until startup completes." {
		t.Fatalf("disabled decision = %#v", disabled)
	}
	ignored := DecideReasoningShortcut(ReasoningShortcutRaise, ReasoningShortcutContext{ModalOrPopupActive: true})
	if ignored.Handled || ignored.Action != ReasoningShortcutIgnored {
		t.Fatalf("ignored decision = %#v", ignored)
	}
}
