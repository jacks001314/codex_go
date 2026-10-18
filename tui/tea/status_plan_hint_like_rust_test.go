package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

func planHintModel(width int) *Model {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-plan")
	state.SetStatus("idle")
	state.Model = "gpt-5"
	state.PlanMode = true
	return NewModel(state, Options{
		Width:           width,
		Height:          24,
		FeatureSettings: map[string]bool{"collaboration_modes": true},
	})
}

// TestStatusHeaderShowsPlanModeCycleHintLikeRust pins #49037: an idle,
// editable fullscreen composer explains how to leave Plan mode when the full
// hint fits, and falls back to the compact mode label on narrower terminals.
func TestStatusHeaderShowsPlanModeCycleHintLikeRust(t *testing.T) {
	model := planHintModel(180)
	header := model.renderStatusHeader()
	full := bottompane.CollaborationModeLabel(bottompane.CollaborationModePlan, true)
	if !strings.Contains(header, full) {
		t.Fatalf("wide status header = %q, want plan cycle hint %q", header, full)
	}
	if !strings.HasSuffix(header, full) {
		t.Fatalf("plan hint must be right-aligned: %q", header)
	}

	model.width = 56
	narrow := model.renderStatusHeader()
	if !strings.Contains(narrow, "Plan mode") {
		t.Fatalf("narrow status header = %q, want Plan mode", narrow)
	}
	if strings.Contains(narrow, "shift+tab") {
		t.Fatalf("narrow status header kept the cycle hint: %q", narrow)
	}
}

// TestStatusHeaderHidesPlanCycleHintWhenUnavailableLikeRust covers the gates
// upstream applies before showing the hint: running turns, interactive footer
// surfaces, read-only input and disabled collaboration modes keep only the
// compact mode indicator or omit it entirely.
func TestStatusHeaderHidesPlanCycleHintWhenUnavailableLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*Model)
	}{
		{"running", func(model *Model) { model.State.SetStatus("running") }},
		{"popup", func(model *Model) { model.slashPopup.Active = true }},
		{"interactive footer", func(model *Model) { model.escBacktrackHint = true }},
		{"read only", func(model *Model) { model.readOnlyThread = true }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			model := planHintModel(180)
			testCase.mutate(model)
			header := model.renderStatusHeader()
			if strings.Contains(header, "shift+tab") {
				t.Fatalf("status header = %q, cycle hint should be hidden", header)
			}
		})
	}

	model := planHintModel(180)
	model.featureSettings = map[string]bool{"collaboration_modes": false}
	if header := model.renderStatusHeader(); strings.Contains(header, "Plan mode") {
		t.Fatalf("disabled collaboration modes kept Plan mode: %q", header)
	}
}
