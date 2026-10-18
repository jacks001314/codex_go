package appserver

import (
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/prompt"
	"codex_go/turn"
)

// TestCollaborationModeUpdatePlanInstructionsLikeRust mirrors Rust #41744:
// bundled presets lose their plan-tool section while update_plan is disabled,
// custom text and custom model catalogs stay intact.
func TestCollaborationModeUpdatePlanInstructionsLikeRust(t *testing.T) {
	paramsWith := func(text string) *turn.TurnStartParams {
		return &turn.TurnStartParams{CollaborationMode: map[string]any{
			"mode": "plan",
			"settings": map[string]any{
				"developer_instructions": text,
			},
		}}
	}
	bundledPlan := prompt.BuiltinPlanModeInstructions()
	got, ok := collaborationModeInstructions(paramsWith(bundledPlan), nil, false)
	if !ok || strings.Contains(got, "## Plan Mode vs update_plan tool") {
		t.Fatalf("disabled built-in plan preset = %q, ok=%v", got, ok)
	}
	got, ok = collaborationModeInstructions(paramsWith(bundledPlan), nil, true)
	if !ok || !strings.Contains(got, "## Plan Mode vs update_plan tool") {
		t.Fatalf("enabled built-in plan preset = %q, ok=%v", got, ok)
	}
	custom := "## Plan tool\nKeep my custom plan guidance."
	got, ok = collaborationModeInstructions(paramsWith(custom), nil, false)
	if !ok || got != custom {
		t.Fatalf("custom plan text = %q, ok=%v", got, ok)
	}

	catalogPlan := "## Plan Mode vs update_plan tool\nCatalog plan guidance."
	bundledInfo := &model.ModelInfo{ModelMessages: &model.ModelMessages{
		CollaborationModes: &model.CollaborationModeMessages{Plan: &catalogPlan},
	}}
	got, ok = collaborationModeInstructions(paramsWith("ignored"), bundledInfo, false)
	if !ok || strings.Contains(got, "## Plan Mode vs update_plan tool") {
		t.Fatalf("bundled catalog plan instructions = %q, ok=%v", got, ok)
	}
	customInfo := &model.ModelInfo{ModelMessages: &model.ModelMessages{
		InstructionsTemplate: "custom base",
		CollaborationModes:   &model.CollaborationModeMessages{Plan: &catalogPlan},
	}}
	got, ok = collaborationModeInstructions(paramsWith("ignored"), customInfo, false)
	if !ok || got != catalogPlan {
		t.Fatalf("custom catalog plan instructions = %q, ok=%v", got, ok)
	}
}
