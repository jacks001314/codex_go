package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func modelSelectionEditValues(t *testing.T, edits []SettingsEdit) map[string]any {
	t.Helper()
	values := make(map[string]any, len(edits))
	for _, edit := range edits {
		values[edit.KeyPath] = edit.Value
	}
	return values
}

// TestResolveModelSelectionMirrorsRustPickerEvents covers the Rust fan-out a
// completed model/effort picker decision produces: the thread-settings sync
// plus the user-config writes (PersistModelSelection /
// PersistPlanModeReasoningEffort / the effort-only Reserve update).
func TestResolveModelSelectionMirrorsRustPickerEvents(t *testing.T) {
	t.Run("model and reasoning", func(t *testing.T) {
		plan, ok := ResolveModelSelection(&PickerDecision{Kind: "model_reasoning", Value: "gpt-5", ReasoningEffort: "high"})
		if !ok {
			t.Fatal("decision was not resolved")
		}
		if plan.ThreadModel == nil || *plan.ThreadModel != "gpt-5" {
			t.Fatalf("thread model = %v", plan.ThreadModel)
		}
		if plan.ThreadEffort == nil || *plan.ThreadEffort != "high" {
			t.Fatalf("thread effort = %v", plan.ThreadEffort)
		}
		if len(plan.Persist) != 1 || plan.Persist[0].Label != modelSelectionPersistLabel {
			t.Fatalf("persist = %#v", plan.Persist)
		}
		values := modelSelectionEditValues(t, plan.Persist[0].Edits)
		if values["model"] != "gpt-5" || values["model_reasoning_effort"] != "high" {
			t.Fatalf("edits = %#v", values)
		}
	})

	t.Run("empty effort clears the saved default", func(t *testing.T) {
		plan, ok := ResolveModelSelection(&PickerDecision{Kind: "model", Value: "gpt-5"})
		if !ok {
			t.Fatal("decision was not resolved")
		}
		if plan.ThreadEffort != nil {
			t.Fatalf("thread effort = %v, want none", *plan.ThreadEffort)
		}
		values := modelSelectionEditValues(t, plan.Persist[0].Edits)
		if values["model"] != "gpt-5" || values["model_reasoning_effort"] != nil {
			t.Fatalf("edits = %#v", values)
		}
	})

	t.Run("reserve updates effort without saving a default", func(t *testing.T) {
		plan, ok := ResolveModelSelection(&PickerDecision{Kind: "model_reasoning", Value: LunaReserveModel, ReasoningEffort: "medium"})
		if !ok {
			t.Fatal("decision was not resolved")
		}
		if plan.ThreadModel != nil {
			t.Fatalf("reserve thread model = %v, want none", *plan.ThreadModel)
		}
		if plan.ThreadEffort == nil || *plan.ThreadEffort != "medium" {
			t.Fatalf("thread effort = %v", plan.ThreadEffort)
		}
		if len(plan.Persist) != 0 {
			t.Fatalf("reserve persist = %#v, want none", plan.Persist)
		}
	})

	t.Run("plan scope plan only", func(t *testing.T) {
		plan, ok := ResolveModelSelection(&PickerDecision{
			Kind:            "plan_reasoning_scope",
			Value:           "gpt-5",
			ReasoningEffort: "high",
			Scope:           string(codextui.PlanReasoningScopePlanOnly),
		})
		if !ok {
			t.Fatal("decision was not resolved")
		}
		if plan.ThreadModel == nil || *plan.ThreadModel != "gpt-5" || plan.ThreadEffort == nil || *plan.ThreadEffort != "high" {
			t.Fatalf("thread plan-only = %#v", plan)
		}
		if len(plan.Persist) != 1 || plan.Persist[0].Label != planModeReasoningPersistLabel {
			t.Fatalf("plan-only persist = %#v", plan.Persist)
		}
		if values := modelSelectionEditValues(t, plan.Persist[0].Edits); values["plan_mode_reasoning_effort"] != "high" {
			t.Fatalf("plan-only edits = %#v", values)
		}
	})

	t.Run("plan scope all modes also persists the model default", func(t *testing.T) {
		plan, ok := ResolveModelSelection(&PickerDecision{
			Kind:            "plan_reasoning_scope",
			Value:           "gpt-5",
			ReasoningEffort: "high",
			Scope:           string(codextui.PlanReasoningScopeAllModes),
		})
		if !ok {
			t.Fatal("decision was not resolved")
		}
		if len(plan.Persist) != 2 {
			t.Fatalf("all-modes persist = %#v", plan.Persist)
		}
		if plan.Persist[0].Label != planModeReasoningPersistLabel || plan.Persist[1].Label != modelSelectionPersistLabel {
			t.Fatalf("all-modes persist labels = %#v", plan.Persist)
		}
		values := modelSelectionEditValues(t, plan.Persist[1].Edits)
		if values["model"] != "gpt-5" || values["model_reasoning_effort"] != "high" {
			t.Fatalf("all-modes model edits = %#v", values)
		}
	})

	t.Run("rate limit switch updates the thread without persisting", func(t *testing.T) {
		plan, ok := ResolveModelSelection(&PickerDecision{Kind: "rate_limit_switch_model", Value: "gpt-5-mini", ReasoningEffort: "low"})
		if !ok {
			t.Fatal("decision was not resolved")
		}
		if plan.ThreadModel == nil || *plan.ThreadModel != "gpt-5-mini" {
			t.Fatalf("thread model = %v", plan.ThreadModel)
		}
		if len(plan.Persist) != 0 {
			t.Fatalf("rate limit persist = %#v, want none", plan.Persist)
		}
	})

	t.Run("non model decisions are ignored", func(t *testing.T) {
		for _, decision := range []*PickerDecision{
			nil,
			{Kind: string(codextui.SessionSelectionResume), Value: "thread-1"},
			{Kind: "model", Value: "   "},
		} {
			if _, ok := ResolveModelSelection(decision); ok {
				t.Fatalf("decision %#v was resolved", decision)
			}
		}
	})
}

// TestModelSelectionSyncSurfacesFailuresAndOverrides covers the notice the Rust
// app adds when the thread-settings sync fails, the config write fails, or a
// higher-priority layer overrides the saved default.
func TestModelSelectionSyncSurfacesFailuresAndOverrides(t *testing.T) {
	view := func(model *Model) string {
		model.refreshTranscript()
		return model.View()
	}

	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 40})
	model.applyModelSelectionSync(ModelSelectionSyncMsg{
		ThreadSettingsErr: errors.New("offline"),
		PersistLabel:      modelSelectionPersistLabel,
	})
	if got := view(model); !strings.Contains(got, "Failed to update thread settings: offline") {
		t.Fatalf("thread settings failure missing:\n%s", got)
	}

	model = NewModel(codextui.NewState(nil), Options{Width: 100, Height: 40})
	model.applyModelSelectionSync(ModelSelectionSyncMsg{
		PersistLabel: planModeReasoningPersistLabel,
		PersistErr:   errors.New("disk full"),
	})
	if got := view(model); !strings.Contains(got, "Failed to save Plan mode reasoning effort: disk full") {
		t.Fatalf("persist failure missing:\n%s", got)
	}

	model = NewModel(codextui.NewState(nil), Options{Width: 100, Height: 40})
	model.applyModelSelectionSync(ModelSelectionSyncMsg{
		PersistLabel:      modelSelectionPersistLabel,
		PersistOverridden: true,
	})
	if got := view(model); !strings.Contains(got, "Saved default model and reasoning effort") ||
		!strings.Contains(got, "higher-priority configuration layer overrides the") {
		t.Fatalf("override warning missing:\n%s", got)
	}
}
