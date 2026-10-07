package appserver

import "sort"

// environmentCapabilityRootEntry keeps one selected root together with its
// position in the thread's retained root list.
type environmentCapabilityRootEntry struct {
	index int
	root  SelectedCapabilityRoot
}

// EnvironmentCapabilityRoots holds the capability roots selected for one
// environment, retaining each root's position in the thread's original root
// list. Mirrors Rust `codex_protocol::capabilities::EnvironmentCapabilityRoots`
// (#51493).
//
// Executor skills assign aliases (`e0`, `e1`, ...) in root order, so splitting
// roots across environments and combining them again must not change that
// order.
type EnvironmentCapabilityRoots struct {
	entries []environmentCapabilityRootEntry
}

// CapabilityRootsForEnvironment selects the roots whose location names
// environmentID, keeping each root's original index. Mirrors Rust
// `EnvironmentCapabilityRoots::for_environment`. Roots for other environments
// and roots with a non-environment location are dropped.
func CapabilityRootsForEnvironment(environmentID string, roots []SelectedCapabilityRoot) EnvironmentCapabilityRoots {
	selected := make([]environmentCapabilityRootEntry, 0, len(roots))
	for index, root := range roots {
		if root.Location.Type != CapabilityRootLocationEnvironment {
			continue
		}
		if root.Location.EnvironmentID != environmentID {
			continue
		}
		selected = append(selected, environmentCapabilityRootEntry{index: index, root: root})
	}
	return EnvironmentCapabilityRoots{entries: selected}
}

// CollectEnvironmentCapabilityRoots restores the original root order across the
// given environment selections, so changing environment order does not change
// skill aliases. Mirrors Rust `EnvironmentCapabilityRoots::collect`.
func CollectEnvironmentCapabilityRoots(selections []EnvironmentCapabilityRoots) []SelectedCapabilityRoot {
	entries := make([]environmentCapabilityRootEntry, 0, len(selections))
	for _, selection := range selections {
		entries = append(entries, selection.entries...)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].index < entries[j].index })
	roots := make([]SelectedCapabilityRoot, 0, len(entries))
	for _, entry := range entries {
		roots = append(roots, entry.root)
	}
	return roots
}

// restrictCapabilityRootsToSelections keeps only the roots that belong to one of
// the selected environments, restoring the thread's original root order. It
// mirrors Rust `TurnEnvironmentSnapshot::selected_capability_roots` (#51493):
// capability discovery must stay within the environments captured by the
// current turn, and reselecting an environment restores its roots because the
// thread's retained root list is unchanged.
//
// A thread without any environment selection keeps its full root list: the Go
// port accepts roots at thread start without tying them to a selection, and the
// app-server normalizes an empty selection list to "unset"
// (persistThreadEnvironmentSelections deletes the key). Rust always starts a
// thread with at least one selection, so it can observe an explicit empty set.
func restrictCapabilityRootsToSelections(roots []SelectedCapabilityRoot, selections []map[string]any) []SelectedCapabilityRoot {
	if len(roots) == 0 || len(selections) == 0 {
		return roots
	}
	perEnvironment := make([]EnvironmentCapabilityRoots, 0, len(selections))
	for _, selection := range selections {
		perEnvironment = append(perEnvironment, CapabilityRootsForEnvironment(selectionEnvironmentID(selection), roots))
	}
	return CollectEnvironmentCapabilityRoots(perEnvironment)
}
