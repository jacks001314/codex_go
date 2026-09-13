package tea

import (
	"strings"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

const (
	// modelSelectionPersistLabel mirrors Rust's persist_model_defaults setting
	// name ("default model and reasoning effort").
	modelSelectionPersistLabel = "default model and reasoning effort"
	// planModeReasoningPersistLabel mirrors Rust's PersistPlanModeReasoningEffort
	// setting name.
	planModeReasoningPersistLabel = "Plan mode reasoning effort"
)

// ModelSelectionPersist is one user-config write a model/effort selection maps
// to (Rust config_update::build_model_selection_edits and the
// PersistPlanModeReasoningEffort edit).
type ModelSelectionPersist struct {
	// Label names the setting in the save/override messages.
	Label string
	// Edits are the config edits to write; a nil edit value clears the key.
	Edits []SettingsEdit
}

// ModelSelectionPlan is the thread-settings sync plus the user-config writes a
// completed model/effort picker decision maps to. It mirrors the Rust app's
// UpdateModel / UpdateReasoningEffort / PersistModelSelection fan-out in
// app/model_popups.rs and app/event_dispatch.rs.
type ModelSelectionPlan struct {
	// ThreadModel is nil when the decision does not change the thread model
	// (Rust's effort-only Reserve update).
	ThreadModel *string
	// ThreadEffort is nil when the decision does not change the effort.
	ThreadEffort *string
	// Persist lists the user-config writes to perform, in order.
	Persist []ModelSelectionPersist
}

// ResolveModelSelection maps a picker decision to the settings sync and config
// writes Rust performs. The second result is false for decisions that do not
// select a model or reasoning effort (session, theme, and other pickers).
func ResolveModelSelection(decision *PickerDecision) (ModelSelectionPlan, bool) {
	if decision == nil {
		return ModelSelectionPlan{}, false
	}
	model := strings.TrimSpace(decision.Value)
	effort := strings.TrimSpace(decision.ReasoningEffort)
	switch decision.Kind {
	case "model", "model_reasoning":
		if model == "" {
			return ModelSelectionPlan{}, false
		}
		plan := ModelSelectionPlan{ThreadEffort: optionalEffort(effort)}
		if model == LunaReserveModel {
			// Reserve is temporary: update the active task's effort without
			// persisting a model default (Rust update_luna_reserve_reasoning).
			return plan, true
		}
		plan.ThreadModel = &model
		plan.Persist = append(plan.Persist, ModelSelectionPersist{
			Label: modelSelectionPersistLabel,
			Edits: modelSelectionEdits(model, effort),
		})
		return plan, true
	case "plan_reasoning_scope":
		if model == "" {
			return ModelSelectionPlan{}, false
		}
		plan := ModelSelectionPlan{ThreadModel: &model, ThreadEffort: optionalEffort(effort)}
		plan.Persist = append(plan.Persist, ModelSelectionPersist{
			Label: planModeReasoningPersistLabel,
			Edits: []SettingsEdit{{KeyPath: "plan_mode_reasoning_effort", Value: nullableEffort(effort)}},
		})
		if decision.Scope == string(codextui.PlanReasoningScopeAllModes) {
			plan.Persist = append(plan.Persist, ModelSelectionPersist{
				Label: modelSelectionPersistLabel,
				Edits: modelSelectionEdits(model, effort),
			})
		}
		return plan, true
	case "rate_limit_switch_model":
		if model == "" {
			return ModelSelectionPlan{}, false
		}
		// The rate-limit nudge updates the thread context without persisting a
		// default (Rust chatwidget/rate_limits.rs).
		return ModelSelectionPlan{ThreadModel: &model, ThreadEffort: optionalEffort(effort)}, true
	default:
		return ModelSelectionPlan{}, false
	}
}

// modelSelectionEdits mirrors Rust's build_model_selection_edits: replace the
// model and replace-or-clear the reasoning effort.
func modelSelectionEdits(model string, effort string) []SettingsEdit {
	return []SettingsEdit{
		{KeyPath: "model", Value: model},
		{KeyPath: "model_reasoning_effort", Value: nullableEffort(effort)},
	}
}

func optionalEffort(effort string) *string {
	if effort == "" {
		return nil
	}
	copied := effort
	return &copied
}

func nullableEffort(effort string) any {
	if effort == "" {
		return nil
	}
	return effort
}

// ModelSelectionSyncMsg reports the outcome of applying a model/effort picker
// decision to the app-server thread and to the persisted user defaults.
type ModelSelectionSyncMsg struct {
	// ThreadSettingsErr is the thread/settings/update failure, if any.
	ThreadSettingsErr error
	// PersistLabel names the setting for the save failure and override notices.
	PersistLabel string
	// PersistErr is the config write failure, if any.
	PersistErr error
	// PersistOverridden is set when the write succeeded but a higher-priority
	// configuration layer overrides the saved value.
	PersistOverridden bool
}

// applyModelSelectionSync surfaces the sync/persistence outcome the way Rust's
// event handlers do: a thread-settings failure and a save failure each add an
// error message, and an overridden write adds a warning.
func (m *Model) applyModelSelectionSync(msg ModelSelectionSyncMsg) {
	if m == nil {
		return
	}
	if msg.ThreadSettingsErr != nil {
		m.addErrorHistoryMessage("Failed to update thread settings: " + strings.TrimSpace(msg.ThreadSettingsErr.Error()))
	}
	if msg.PersistErr != nil {
		m.addErrorHistoryMessage("Failed to save " + msg.PersistLabel + ": " + strings.TrimSpace(msg.PersistErr.Error()))
		m.refreshTranscript()
		return
	}
	if msg.PersistOverridden {
		m.applyHistoryCell(historycell.NewWarningEvent(
			"Saved " + msg.PersistLabel + ", but a higher-priority configuration layer overrides the saved value."))
	}
	m.refreshTranscript()
}
