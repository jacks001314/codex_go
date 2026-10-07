package rollout

import "strings"

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
