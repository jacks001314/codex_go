package appserver

import (
	"testing"

	"codex_go/model"
	"codex_go/turn"
)

func stepToolPlanCatalog() model.ModelsResponse {
	return model.ModelsResponse{Models: []model.ModelInfo{
		{
			Slug:                       "turn-model",
			DisplayName:                "Turn Model",
			Visibility:                 model.VisibilityList,
			SupportedInAPI:             true,
			ApplyPatchToolType:         "freeform",
			ToolMode:                   model.ToolModeDirect,
			ExperimentalSupportedTools: []string{"clock"},
		},
		{
			Slug:           "step-model",
			DisplayName:    "Step Model",
			Visibility:     model.VisibilityList,
			SupportedInAPI: true,
			ToolMode:       model.ToolModeCodeModeOnly,
		},
	}}
}

func stepToolPlanTestRouter(t *testing.T) *RuntimeRouter {
	t.Helper()
	return NewRuntimeRouter(RuntimeServices{
		Models: model.NewModelService(model.NewStaticModelsManager(stepToolPlanCatalog())),
	})
}

// Rust #44242 plans a later sampling step from that step's captured settings.
// The Go host keeps the turn's assembled tool options and re-derives only the
// model-dependent half for the step's model, leaving the turn's template alone.
func TestStepToolRegistryOptionsFollowTheStepModelLikeRust(t *testing.T) {
	router := stepToolPlanTestRouter(t)
	cwd := t.TempDir()
	threadID, turnID := "thread-step-plan", "turn-step-plan"
	template := turn.DefaultToolRegistryOptions(cwd)
	template.Model = "turn-model"
	template.ToolMode = model.ToolModeDirect
	template.EnableApplyPatch = true
	template.ExperimentalSupportedTools = []string{"clock"}
	router.rememberTurnToolOptions(threadID, turnID, cwd, &turn.TurnStartParams{ThreadID: threadID, Model: "turn-model"}, nil, template)

	planForStep := router.stepToolPlanForTurn(threadID, turnID)
	if planForStep == nil {
		t.Fatalf("stepToolPlanForTurn() = nil, want a plan builder")
	}
	restored, err := planForStep("turn-model")
	if err != nil || restored == nil || restored.Router == nil {
		t.Fatalf("turn-model step plan = %#v, %v; want the turn plan restored", restored, err)
	}
	if restored.Router.ToolMode() != model.ToolModeDirect {
		t.Fatalf("restored router tool mode = %q, want the turn plan", restored.Router.ToolMode())
	}
	inputs, ok := router.rememberedTurnToolInputs(threadID, turnID)
	if !ok {
		t.Fatalf("rememberedTurnToolInputs() = missing, want the turn's inputs")
	}
	options, hostedTools, err := router.stepToolRegistryOptions(inputs, "step-model")
	if err != nil {
		t.Fatalf("stepToolRegistryOptions() error = %v", err)
	}
	if options.Model != "step-model" {
		t.Fatalf("step options model = %q, want the step model", options.Model)
	}
	if options.ToolMode != model.ToolModeCodeModeOnly {
		t.Fatalf("step options tool mode = %q, want %q", options.ToolMode, model.ToolModeCodeModeOnly)
	}
	if options.EnableApplyPatch {
		t.Fatalf("step options still advertise apply_patch for a model without apply_patch_tool_type")
	}
	if len(options.ExperimentalSupportedTools) != 0 {
		t.Fatalf("step experimental tools = %#v, want the step catalog's entries", options.ExperimentalSupportedTools)
	}
	if len(hostedTools) != 0 {
		t.Fatalf("step hosted tools = %#v, want none without a config", hostedTools)
	}
	if template.Model != "turn-model" || template.ToolMode != model.ToolModeDirect || !template.EnableApplyPatch || len(template.ExperimentalSupportedTools) != 1 {
		t.Fatalf("turn template mutated by the step rebuild: %#v", template)
	}

	plan, err := router.buildStepToolPlan(inputs, "step-model")
	if err != nil {
		t.Fatalf("buildStepToolPlan() error = %v", err)
	}
	if plan == nil || plan.Router == nil {
		t.Fatalf("buildStepToolPlan() = %#v, want a rebuilt router", plan)
	}
	if !plan.ReplaceHostedTools {
		t.Fatalf("step plan does not replace the turn's hosted tools")
	}
	if plan.Router.ToolMode() != model.ToolModeCodeModeOnly {
		t.Fatalf("rebuilt router tool mode = %q, want %q", plan.Router.ToolMode(), model.ToolModeCodeModeOnly)
	}
}

// A step only rebuilds when the turn actually captured tool inputs and the
// step's model differs from the turn's; otherwise the turn plan stands.
func TestStepToolPlanRequiresTheTurnsOwnToolInputsLikeRust(t *testing.T) {
	router := stepToolPlanTestRouter(t)
	if plan, err := router.stepToolPlanForTurn("thread-without-inputs", "turn-1")("step-model"); err != nil || plan != nil {
		t.Fatalf("plan without turn inputs = %#v, %v; want the turn plan kept", plan, err)
	}

	cwd := t.TempDir()
	threadID := "thread-other-turn"
	template := turn.DefaultToolRegistryOptions(cwd)
	router.rememberTurnToolOptions(threadID, "turn-1", cwd, &turn.TurnStartParams{ThreadID: threadID, Model: "turn-model"}, nil, template)
	if plan, err := router.stepToolPlanForTurn(threadID, "turn-2")("step-model"); err != nil || plan != nil {
		t.Fatalf("plan from another turn's inputs = %#v, %v; want the turn plan kept", plan, err)
	}
}
