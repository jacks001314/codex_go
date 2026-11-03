package app

import (
	"testing"

	chatwidget "codex_go/tui/chatwidget"
)

// Rust parity: #42401 (d4dc882998). The embedded TUI reads its collaboration
// modes from the app server's collaborationMode/list catalog instead of the
// builtin presets, so plan mode is exactly what the server advertises.
func TestInteractiveLocalListCollaborationModesLikeRust(t *testing.T) {
	masks, err := interactiveLocalListCollaborationModes()()
	if err != nil {
		t.Fatalf("collaborationMode/list = %v", err)
	}
	if len(masks) == 0 {
		t.Fatal("the app server advertised no collaboration modes")
	}
	plan, ok := chatwidget.PlanCollaborationMask(masks)
	if !ok {
		t.Fatalf("plan mode missing from the server catalog: %#v", masks)
	}
	if plan.ReasoningEffort.Value == nil || *plan.ReasoningEffort.Value != chatwidget.CollaborationPlanDefaultReasoningEffort {
		t.Fatalf("plan reasoning effort = %#v", plan.ReasoningEffort)
	}
	if _, ok := chatwidget.DefaultModeCollaborationMask(masks); !ok {
		t.Fatalf("default mode missing from the server catalog: %#v", masks)
	}
}
