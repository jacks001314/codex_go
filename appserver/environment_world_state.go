package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/session"
)

// environmentWorldStateSnapshot mirrors the removal-sensitive fields of Rust's
// EnvironmentsSnapshot (codex-rs/core/src/context/world_state/environment.rs).
// This runtime always renders the full environment context, so the snapshot
// only records the values whose disappearance is reported to the model.
type environmentWorldStateSnapshot struct {
	CurrentDate  string `json:"currentDate,omitempty"`
	ShellVersion string `json:"shellVersion,omitempty"`
}

// environmentWorldStateRemovals reports which previously visible environment
// values are gone from the context about to be rendered.
type environmentWorldStateRemovals struct {
	CurrentDate  bool
	ShellVersion bool
}

// decodeEnvironmentWorldStateSnapshot restores the stored environment section.
// An absent or undecodable section has no known previous values, which Rust
// models as PreviousSectionState::Absent/Unknown: neither reports a removal
// (environment.rs render_diff maps both to the default snapshot).
func decodeEnvironmentWorldStateSnapshot(raw json.RawMessage) (environmentWorldStateSnapshot, bool) {
	if len(raw) == 0 {
		return environmentWorldStateSnapshot{}, false
	}
	var snapshot environmentWorldStateSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return environmentWorldStateSnapshot{}, false
	}
	return snapshot, true
}

// recordEnvironmentWorldState mirrors Rust's EnvironmentsState::render_diff
// snapshot bookkeeping (#45825): it compares the values the model is about to
// see with the values it already saw and records the new snapshot. A value that
// was visible before and is gone now renders as
// `<current_date status="unavailable" />` (or `<shell_version ... />`) exactly
// once, because the empty snapshot makes the next comparison a no-op; a value
// that comes back renders normally again.
//
// emitRemoval is false for the compaction window, where Rust re-renders the
// whole world state (render_full) so a missing value is already explicit.
// save persists the updated world state; the compaction path leaves the record
// for its caller to store.
func (r *RuntimeRouter) recordEnvironmentWorldState(record *session.Record, currentDate string, shellVersion string, emitRemoval bool, save bool) (environmentWorldStateRemovals, error) {
	currentDate = strings.TrimSpace(currentDate)
	shellVersion = strings.TrimSpace(shellVersion)
	current := environmentWorldStateSnapshot{CurrentDate: currentDate, ShellVersion: shellVersion}
	removals := environmentWorldStateRemovals{}
	if record == nil {
		return removals, nil
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return removals, err
	}
	previous, known := decodeEnvironmentWorldStateSnapshot(state.Environments)
	if known && emitRemoval {
		removals.CurrentDate = previous.CurrentDate != "" && currentDate == ""
		removals.ShellVersion = previous.ShellVersion != "" && shellVersion == ""
	}
	snapshot, err := json.Marshal(current)
	if err != nil {
		return removals, err
	}
	if sameJSONValue(state.Environments, snapshot) {
		return removals, nil
	}
	state.Environments = json.RawMessage(snapshot)
	record.Metadata.WorldState, err = session.EncodeWorldState(state)
	if err != nil {
		return removals, err
	}
	if save && r != nil {
		if err := r.runtimeSaveThreadRecord(record); err != nil {
			return removals, err
		}
	}
	return removals, nil
}
