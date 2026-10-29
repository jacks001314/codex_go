package bottompane

import (
	"strings"
	"testing"

	"codex_go/features"
	"codex_go/tui"
)

// TestExperimentalFeaturesAnalyticsPlanHistoryLikeRust pins Rust #45772's
// experimental-menu snapshot: plan history appears unchecked with its preview
// copy.
func TestExperimentalFeaturesAnalyticsPlanHistoryLikeRust(t *testing.T) {
	const key = "analytics_plan_history"
	registry := features.Registry
	var spec features.Spec
	for _, candidate := range registry {
		if candidate.Key == key {
			spec = candidate
			break
		}
	}
	if spec.Stage != features.StageExperimental || spec.ExperimentalName == "" || spec.ExperimentalMenuDescription == "" {
		t.Fatalf("experimental registry spec = %#v", spec)
	}
	view := NewExperimentalFeaturesView([]ExperimentalFeatureItem{{
		Feature:     key,
		Name:        spec.ExperimentalName,
		Description: spec.ExperimentalMenuDescription,
		Enabled:     spec.DefaultEnabled,
	}})
	rows := view.Rows(240)
	joined := strings.Join(rows, "\n")
	if !strings.Contains(joined, "[ ] "+spec.ExperimentalName) {
		t.Fatalf("experimental menu missing the unchecked entry:\n%s", joined)
	}
	if !strings.Contains(joined, spec.ExperimentalMenuDescription) {
		t.Fatalf("experimental menu missing the preview copy:\n%s", joined)
	}
}

func TestExperimentalFeaturesViewToggleSaveRowsMatchRust(t *testing.T) {
	view := NewExperimentalFeaturesView([]ExperimentalFeatureItem{
		{Feature: "fast-mode", Name: "Fast mode", Description: "Use faster inference", Enabled: false},
		{Feature: "pets", Name: "Pets", Description: "Show terminal pet", Enabled: true},
	})
	rows := view.Rows(80)
	if !bottomPaneContainsRow(rows, tui.RenderSelectedRow("\u203a [ ] Fast mode  Use faster inference")) {
		t.Fatalf("rows missing selected feature:\n%s", strings.Join(rows, "\n"))
	}
	view.HandleKey("space")
	view.HandleKey("down")
	view.HandleKey("space")
	if !view.Features[0].Enabled || view.Features[1].Enabled {
		t.Fatalf("features after toggle = %#v", view.Features)
	}
	view.HandleKey("enter")
	if !view.Complete || len(view.Events) != 1 {
		t.Fatalf("view complete=%v events=%#v", view.Complete, view.Events)
	}
	if !view.Events[0].Updates["fast-mode"] || view.Events[0].Updates["pets"] {
		t.Fatalf("updates = %#v", view.Events[0].Updates)
	}
}

func TestExperimentalFeaturesEmptyAndNavigation(t *testing.T) {
	view := NewExperimentalFeaturesView(nil)
	if rows := view.Rows(80); !bottomPaneContainsRow(rows, "  No experimental features available for now") {
		t.Fatalf("empty rows = %#v", rows)
	}
	view.HandleKey("esc")
	if !view.Complete || len(view.Events) != 0 {
		t.Fatalf("empty close complete=%v events=%#v", view.Complete, view.Events)
	}

	view = NewExperimentalFeaturesView([]ExperimentalFeatureItem{
		{Feature: "a", Name: "A"},
		{Feature: "b", Name: "B"},
	})
	view.HandleKey("end")
	if view.State.SelectedIdx != 1 {
		t.Fatalf("selected after end = %d", view.State.SelectedIdx)
	}
	view.HandleKey("up")
	if view.State.SelectedIdx != 0 {
		t.Fatalf("selected after up = %d", view.State.SelectedIdx)
	}
}
