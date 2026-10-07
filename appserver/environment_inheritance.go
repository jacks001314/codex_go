package appserver

import (
	"strings"

	"codex_go/agent"
	"codex_go/session"
)

// inheritableEnvironmentSelections mirrors Rust
// `TurnEnvironmentSnapshot::inheritable_selections` (#49075): a native child
// thread retains the ready and still-starting attachments captured by the step
// that spawned it, while a failed attachment is dropped instead of being
// inherited. Thread-derived (`from_thread`) attachments are kept so the child
// keeps following its own thread configuration.
func inheritableEnvironmentSelections(selections []map[string]any) []map[string]any {
	if len(selections) == 0 {
		return cloneMapSlice(selections)
	}
	out := make([]map[string]any, 0, len(selections))
	for _, selection := range selections {
		state, err := environmentConfigStateFromAnyMap(selection)
		if err == nil && state.Kind == EnvironmentConfigFailed {
			continue
		}
		out = append(out, cloneAnyMap(selection))
	}
	return out
}

// resolvedEnvironmentAttachmentState reports the attachment state a thread can
// hand down to its descendants: only an owner-supplied ready or failed
// configuration is a result, while `pending` and `from_thread` attachments are
// still waiting for their owner (Rust EnvironmentConfigState, #49075).
func resolvedEnvironmentAttachmentState(selection map[string]any) (EnvironmentConfigState, bool) {
	state, err := environmentConfigStateFromAnyMap(selection)
	if err != nil {
		return EnvironmentConfigState{}, false
	}
	if state.Kind != EnvironmentConfigReady && state.Kind != EnvironmentConfigFailed {
		return EnvironmentConfigState{}, false
	}
	return state, true
}

func environmentSelectionCWD(selection map[string]any) string {
	return strings.TrimSpace(threadItemStringFromAnyMap(selection, "cwd", "CWD"))
}

func environmentSelectionWorkspaceRoots(selection map[string]any) []string {
	return stringSliceFromAny(firstNonNil(selection["workspaceRoots"], selection["workspace_roots"]))
}

// sameEnvironmentAttachment mirrors Rust's `update_environment_configuration`
// matcher: environment id, cwd and workspace roots must all agree before an
// owner result applies to a descendant's attachment.
func sameEnvironmentAttachment(left, right map[string]any) bool {
	id := selectionEnvironmentID(left)
	if id == "" || id != selectionEnvironmentID(right) {
		return false
	}
	if environmentSelectionCWD(left) != environmentSelectionCWD(right) {
		return false
	}
	leftRoots := environmentSelectionWorkspaceRoots(left)
	rightRoots := environmentSelectionWorkspaceRoots(right)
	if len(leftRoots) != len(rightRoots) {
		return false
	}
	for i := range leftRoots {
		if leftRoots[i] != rightRoots[i] {
			return false
		}
	}
	return true
}

// propagateInheritedEnvironmentConfigurations hands the owner's first accepted
// attachment result down to the descendants that inherited that pending
// attachment, mirroring Rust `Session::follow_inherited_environment_configurations`
// and `update_environment_configuration(.., ConfigUpdateSource::Inherited)`
// (#49075). A descendant only accepts the result while its own attachment is
// still pending, so an independently configured child keeps its own
// configuration and a later owner update never overwrites the first inherited
// one. The update cascades because the descendant persists its own selections,
// which propagates again to its children.
func (r *RuntimeRouter) propagateInheritedEnvironmentConfigurations(ownerThreadID string, selections []map[string]any) {
	if r == nil || len(selections) == 0 {
		return
	}
	ownerThreadID = strings.TrimSpace(ownerThreadID)
	if ownerThreadID == "" {
		return
	}
	carried := make([]map[string]any, 0, len(selections))
	for _, selection := range selections {
		if _, ok := resolvedEnvironmentAttachmentState(selection); !ok {
			continue
		}
		carried = append(carried, selection)
	}
	if len(carried) == 0 {
		return
	}
	for _, childID := range r.openSpawnChildThreadIDs(ownerThreadID) {
		r.applyInheritedEnvironmentConfigurations(childID, carried)
	}
}

// openSpawnChildThreadIDs lists a thread's direct children with an open
// thread-spawn edge. Propagation needs the recorded spawn graph, which the
// runtime wires from its state database (services.SpawnGraph); without a graph
// the pre-#49075 behavior is kept.
func (r *RuntimeRouter) openSpawnChildThreadIDs(parentThreadID string) []string {
	if r == nil || r.services.SpawnGraph == nil {
		return nil
	}
	status := agent.ThreadSpawnEdgeOpen
	children, err := r.services.SpawnGraph.ListThreadSpawnChildren(parentThreadID, &status)
	if err != nil {
		return nil
	}
	return children
}

// applyInheritedEnvironmentConfigurations installs an owner's resolved
// attachment result through the descendant's own validation, matching Rust's
// `update_environment_configuration` with `ConfigUpdateSource::Inherited`: the
// descendant's selection must still be pending, and a result that violates the
// descendant's attachment-config contract is recorded as that descendant's own
// failure instead of being dropped.
func (r *RuntimeRouter) applyInheritedEnvironmentConfigurations(threadID string, resolved []map[string]any) {
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return
	}
	existing := environmentSelectionsFromAny(record.Metadata.Extra[runtimeEnvironmentSelectionsExtraKey])
	if len(existing) == 0 {
		return
	}
	updated := cloneMapSlice(existing)
	changed := false
	for _, candidate := range resolved {
		candidateState, ok := resolvedEnvironmentAttachmentState(candidate)
		if !ok {
			continue
		}
		for index, selection := range updated {
			if !sameEnvironmentAttachment(selection, candidate) {
				continue
			}
			current, err := environmentConfigStateFromAnyMap(selection)
			if err != nil || current.Kind != EnvironmentConfigPending {
				continue
			}
			next := candidateState
			if candidateState.Kind == EnvironmentConfigReady {
				if validateErr := validateEnvironmentConfigForSelection(selectionEnvironmentID(selection), candidateState.Config); validateErr != nil {
					next = EnvironmentConfigState{Kind: EnvironmentConfigFailed, Error: validateErr.Error()}
				}
			}
			merged := cloneAnyMap(selection)
			merged["config"] = environmentConfigStateToAny(next)
			updated[index] = merged
			changed = true
		}
	}
	if !changed {
		return
	}
	_ = r.persistThreadEnvironmentSelections(threadID, updated)
}
