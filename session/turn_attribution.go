package session

import (
	"encoding/json"
	"strings"
)

// TurnAttribution is the provenance of one regular turn, persisted in the
// `turn_started` rollout event and restored during recovery.
//
// Mirrors Rust `codex_protocol::turn_input::TurnAttribution` (Rust #51402
// `551bd409eb`, "Preserve turn attribution across recovery and compaction").
// The field names are the Rust snake_case wire names; every optional field
// round-trips as absent when it was never recorded.
type TurnAttribution struct {
	TurnID              string  `json:"turn_id"`
	TurnTrigger         *string `json:"turn_trigger,omitempty"`
	ParentTurnID        *string `json:"parent_turn_id,omitempty"`
	InitiatingAgentPath *string `json:"initiating_agent_path,omitempty"`
	RootTurnID          *string `json:"root_turn_id,omitempty"`
}

// Valid reports whether the attribution carries the turn identity it describes.
// Rust compares `attribution.turn_id == event.turn_id` before adopting a
// persisted attribution, so an attribution without a turn id is never adopted.
func (a *TurnAttribution) Valid() bool {
	return a != nil && strings.TrimSpace(a.TurnID) != ""
}

// TurnTriggerValue returns the persisted trigger, or "" when absent. An absent
// trigger stays absent during recovery; Rust deliberately keeps unknown
// triggers unknown instead of substituting `retry`.
func (a *TurnAttribution) TurnTriggerValue() string {
	if a == nil || a.TurnTrigger == nil {
		return ""
	}
	return *a.TurnTrigger
}

// RootTurnIDValue returns the persisted root turn id, or "" when absent.
func (a *TurnAttribution) RootTurnIDValue() string {
	if a == nil || a.RootTurnID == nil {
		return ""
	}
	return *a.RootTurnID
}

// ParentTurnIDValue returns the persisted parent turn id, or "" when absent.
func (a *TurnAttribution) ParentTurnIDValue() string {
	if a == nil || a.ParentTurnID == nil {
		return ""
	}
	return *a.ParentTurnID
}

// InitiatingAgentPathValue returns the persisted initiating agent path, or ""
// when absent.
func (a *TurnAttribution) InitiatingAgentPathValue() string {
	if a == nil || a.InitiatingAgentPath == nil {
		return ""
	}
	return *a.InitiatingAgentPath
}

// TurnStartOptions is the provenance a recovered turn restores. Rust's
// `TurnStartOptions` carries the thread's execution settings too; attribution
// only ever restores where the turn came from, never how the thread runs, so
// Go keeps the recovered subset separate (Rust #51402 `TurnAttribution::
// start_options`).
type TurnStartOptions struct {
	TurnTrigger         string
	ParentTurnID        string
	RootTurnID          string
	InitiatingAgentPath string
}

// StartOptions mirrors Rust `TurnAttribution::start_options`: it restores the
// turn's provenance without changing the thread's execution settings. An absent
// field stays absent rather than being defaulted (in particular the trigger is
// never replaced with `retry`).
func (a *TurnAttribution) StartOptions() TurnStartOptions {
	if a == nil {
		return TurnStartOptions{}
	}
	return TurnStartOptions{
		TurnTrigger:         a.TurnTriggerValue(),
		ParentTurnID:        a.ParentTurnIDValue(),
		RootTurnID:          a.RootTurnIDValue(),
		InitiatingAgentPath: a.InitiatingAgentPathValue(),
	}
}

// RecoveredTurnStartOptions mirrors Rust
// `SessionState::recovered_turn_start_options` (Rust #51402): a persisted
// attribution naming this turn restores its provenance; otherwise older
// rollouts only persisted the root on the model-context record, so that root is
// used when the context names this turn.
func (r *Record) RecoveredTurnStartOptions(turnID string) TurnStartOptions {
	turnID = strings.TrimSpace(turnID)
	if r == nil || turnID == "" {
		return TurnStartOptions{}
	}
	if attribution := r.Metadata.TurnAttribution; attribution != nil && strings.TrimSpace(attribution.TurnID) == turnID {
		return attribution.StartOptions()
	}
	return TurnStartOptions{RootTurnID: contextRootTurnIDForTurn(r.Metadata.TurnContext, turnID)}
}

// contextRootTurnIDForTurn reads the root turn id off a model-context record
// when that record names the given turn.
func contextRootTurnIDForTurn(raw json.RawMessage, turnID string) string {
	if len(raw) == 0 {
		return ""
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		return ""
	}
	contextTurnID := ""
	for _, key := range []string{"turn_id", "turnId"} {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			contextTurnID = strings.TrimSpace(value)
			break
		}
	}
	if contextTurnID != turnID {
		return ""
	}
	for _, key := range []string{"root_turn_id", "rootTurnId"} {
		if value, ok := values[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
