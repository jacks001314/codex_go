package tea

import (
	"testing"

	codextui "codex_go/tui"
)

func TestModelPickerAsyncFetchLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	m := NewModel(state, Options{
		OnListModels: func(includeHidden bool) ([]codextui.ModelPickerOption, error) {
			return []codextui.ModelPickerOption{{ID: "gpt-fresh", Label: "Fresh", IsDefault: true}}, nil
		},
	})
	cmd := m.openModelPicker()
	if cmd == nil {
		t.Fatal("openModelPicker did not return a fetch command")
	}
	msg := cmd()
	result, ok := msg.(ModelsResultMsg)
	if !ok || result.Err != nil || len(result.Options) != 1 || result.Options[0].ID != "gpt-fresh" {
		t.Fatalf("fetch result = %#v", msg)
	}
	updated, _ := m.Update(result)
	m = updated.(*Model)
	if len(m.modelPickerOpts) != 1 || m.modelPickerOpts[0].ID != "gpt-fresh" {
		t.Fatalf("modelPickerOpts = %#v", m.modelPickerOpts)
	}
	if m.modal == nil || m.modal.modelPicker == nil || len(m.modal.modelPicker.Options) != 1 || m.modal.modelPicker.Options[0].ID != "gpt-fresh" {
		t.Fatalf("modal model picker = %#v", m.modal)
	}
	if m.pendingModelsRequestID != 0 {
		t.Fatalf("pendingModelsRequestID = %d, want reset to 0", m.pendingModelsRequestID)
	}
}

// TestRefreshServiceTierCommandsLikeRust pins the Rust #41467
// sync_service_tier_commands behavior: the slash-command service-tier entries are
// recomputed from the active model's catalog service tiers when the model
// changes or the catalog refreshes.
func TestRefreshServiceTierCommandsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Model = "gpt-priority"
	m := NewModel(state, Options{})
	m.modelPickerOpts = []codextui.ModelPickerOption{
		{ID: "gpt-priority", Label: "Priority", ServiceTiers: []string{"priority", "default"}},
		{ID: "gpt-basic", Label: "Basic", ServiceTiers: []string{"default"}},
	}
	// The "priority" tier is surfaced as a "fast" command; "default" is implicit.
	m.refreshServiceTierCommands()
	if len(m.serviceTierCommands) != 1 || m.serviceTierCommands[0].ID != "priority" || m.serviceTierCommands[0].Name != "fast" {
		t.Fatalf("serviceTierCommands for gpt-priority = %#v, want lone fast command", m.serviceTierCommands)
	}
	// Switching to a model with only the implicit default tier yields no commands.
	m.State.Model = "gpt-basic"
	m.refreshServiceTierCommands()
	if len(m.serviceTierCommands) != 0 {
		t.Fatalf("serviceTierCommands for gpt-basic = %#v, want empty", m.serviceTierCommands)
	}
}

func TestModelPickerRefreshFallsBackToDefaultLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Model = "gpt-old"
	m := NewModel(state, Options{
		OnListModels: func(includeHidden bool) ([]codextui.ModelPickerOption, error) {
			return []codextui.ModelPickerOption{{ID: "gpt-fresh", Label: "Fresh", IsDefault: true}}, nil
		},
	})
	m.modelPickerOpts = []codextui.ModelPickerOption{{ID: "gpt-stale", Label: "Stale"}}
	cmd := m.openModelPicker()
	if cmd == nil {
		t.Fatal("openModelPicker did not return a fetch command")
	}
	updated, _ := m.Update(cmd())
	m = updated.(*Model)
	if m.State.Model != "gpt-fresh" {
		t.Fatalf("default model after refresh = %q, want gpt-fresh", m.State.Model)
	}
}

func TestModelPickerRefreshPreservesReasoningSubmenuLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	m := NewModel(state, Options{
		OnListModels: func(includeHidden bool) ([]codextui.ModelPickerOption, error) {
			return []codextui.ModelPickerOption{{
				ID: "gpt-a", Label: "A",
				SupportedReasoningEfforts: []codextui.ReasoningEffortOption{{Effort: "low", Label: "Low", IsDefault: true}},
			}}, nil
		},
	})
	m.modelPickerOpts = []codextui.ModelPickerOption{{
		ID: "gpt-a", Label: "A",
		SupportedReasoningEfforts: []codextui.ReasoningEffortOption{{Effort: "low", Label: "Low", IsDefault: true}},
	}}
	m.openModelPicker()
	option := m.modelPickerOpts[0]
	m.openModelReasoningPicker(option)
	if m.modal == nil || m.modal.modelReasoning == nil || len(m.modal.modelReasoning.Options) != 1 {
		t.Fatalf("reasoning picker not opened: %+v", m.modal)
	}
	// Fetch a refreshed catalog where gpt-a supports two reasoning levels.
	cmd := m.fetchModelsForPicker()
	if cmd == nil {
		t.Fatal("fetchModelsForPicker returned nil")
	}
	// Override the fetch result with the updated option.
	updated, _ := m.Update(ModelsResultMsg{
		RequestID: m.pendingModelsRequestID,
		Options: []codextui.ModelPickerOption{{
			ID: "gpt-a", Label: "A",
			SupportedReasoningEfforts: []codextui.ReasoningEffortOption{{Effort: "low", Label: "Low", IsDefault: true}, {Effort: "high", Label: "High"}},
		}},
	})
	m = updated.(*Model)
	if m.modal == nil || m.modal.modelReasoning == nil || len(m.modal.modelReasoning.Options) != 2 {
		t.Fatalf("reasoning picker not refreshed: %+v", m.modal)
	}
}
