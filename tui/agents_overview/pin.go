package agentsoverview

import "strings"

// Shared task pinning for the agent command center (Rust #51500, "Add shared
// task pinning to the agent command center"). Pinned tasks render first in
// their own "Pinned" group, keep the shared section order, and stay subject to
// the dashboard's filters — Rust's search and status filters, of which the Go
// dashboard implements search and hide (the status filter, TASK_FILTERS, has no
// Go counterpart). The host owns the shared thread
// section (Go: appserver/session's PinnedThreadSectionID, reached through
// thread/list + thread/section/move); the core view only owns the pin order and
// the shortcut semantics.

// PinnedGroupHeading is the group header pinned tasks render under (Rust
// agent_center/rows.rs).
const PinnedGroupHeading = "Pinned"

// SupportsSharedPinning reports whether a task's thread source can carry a
// shared pin (Rust agents_overview_discovery::supports_shared_pinning). An empty
// source means the host did not report one, which the dashboard treats as
// unknown and non-pinnable.
func SupportsSharedPinning(source string) bool {
	switch strings.TrimSpace(source) {
	case "cli", "vscode", "exec", "appServer":
		return true
	// Rust also accepts the custom sources "atlas" and "chatgpt".
	case "atlas", "chatgpt":
		return true
	default:
		return false
	}
}

// SetPinnedThreads records the shared pinned-section tasks in section order
// (Rust #51500 list_pinned_threads) and enables pinning. An empty list is still
// "supported": the server answered, it just has no pinned tasks.
func (v *View) SetPinnedThreads(threadIDs []string) {
	if v == nil {
		return
	}
	ranks := make(map[string]int, len(threadIDs))
	for rank, threadID := range threadIDs {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			continue
		}
		if _, ok := ranks[threadID]; ok {
			continue
		}
		ranks[threadID] = rank
	}
	v.pinRanks = ranks
	v.fitSelection()
}

// ClearPinnedThreads disables shared pinning, which the Rust dashboard does when
// the server does not support shared thread sections.
func (v *View) ClearPinnedThreads() {
	if v == nil {
		return
	}
	v.pinRanks = nil
	v.pinPending = false
	v.fitSelection()
}

// PinsSupported reports whether the dashboard learned the shared pinned section
// (Rust pinned_thread_ranks.is_some()).
func (v *View) PinsSupported() bool {
	return v != nil && v.pinRanks != nil
}

// IsPinned reports whether a thread is in the shared pinned section.
func (v *View) IsPinned(threadID string) bool {
	if v == nil || v.pinRanks == nil {
		return false
	}
	_, ok := v.pinRanks[strings.TrimSpace(threadID)]
	return ok
}

// PinnedThreads returns the pinned thread ids in shared-section order.
func (v *View) PinnedThreads() []string {
	if v == nil || len(v.pinRanks) == 0 {
		return nil
	}
	ordered := make([]string, len(v.pinRanks))
	for threadID, rank := range v.pinRanks {
		ordered[rank] = threadID
	}
	return ordered
}

// SetPinPending records whether a pin/unpin request is in flight (Rust
// pin_action_pending). Duplicate pin actions are suppressed while it is set.
func (v *View) SetPinPending(pending bool) {
	if v == nil {
		return
	}
	v.pinPending = pending
}

// PinPending reports whether a pin/unpin request is in flight.
func (v *View) PinPending() bool {
	return v != nil && v.pinPending
}

// ApplyPinChange updates the local pin order after a successful pin/unpin
// (Rust #51500 complete_agents_overview_pin: the shared section is refreshed
// afterwards, this keeps the dashboard correct in the meantime).
func (v *View) ApplyPinChange(threadID string, pinned bool) {
	if v == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	if pinned {
		if v.pinRanks == nil {
			v.pinRanks = map[string]int{}
		}
		if _, ok := v.pinRanks[threadID]; !ok {
			v.pinRanks[threadID] = len(v.pinRanks)
		}
		return
	}
	delete(v.pinRanks, threadID)
}

// CanToggleSelectedPin reports whether the pin shortcut can act on the selected
// task right now (Rust AgentsOverviewView::can_toggle_selected_pin): no pin
// change is pending, shared pinning is available, and the selected task's thread
// source supports shared sections.
func (v *View) CanToggleSelectedPin() bool {
	if v == nil || v.pinPending || v.pinRanks == nil {
		return false
	}
	row := v.SelectedRow()
	if row == nil {
		return false
	}
	return SupportsSharedPinning(row.Source)
}

// TogglePinSelected reports the pin change the dashboard should request for the
// selected task: its thread id and the new pinned state (Rust #51500 keymap
// action agents.toggle_pin, default `p`). ok is false when the pin shortcut is
// not available, including while another pin change is pending, which is how the
// dashboard suppresses duplicate actions.
func (v *View) TogglePinSelected() (threadID string, pinned bool, ok bool) {
	if v == nil || !v.CanToggleSelectedPin() {
		return "", false, false
	}
	row := v.SelectedRow()
	threadID = strings.TrimSpace(row.ThreadID)
	if threadID == "" {
		return "", false, false
	}
	pinned = !v.IsPinned(threadID)
	v.pinPending = true
	return threadID, pinned, true
}

// sortPinnedBySectionPosition orders pinned row indices by their shared-section
// rank (Rust #51500: pinned rows sort by the rank recorded by
// list_pinned_threads).
func (v *View) sortPinnedBySectionPosition(indices []int) {
	if v == nil || len(indices) < 2 {
		return
	}
	rank := func(index int) int {
		if v.pinRanks == nil {
			return 0
		}
		if position, ok := v.pinRanks[strings.TrimSpace(v.Rows[index].ThreadID)]; ok {
			return position
		}
		return len(v.pinRanks)
	}
	for i := 1; i < len(indices); i++ {
		value := indices[i]
		j := i - 1
		for j >= 0 && rank(value) < rank(indices[j]) {
			indices[j+1] = indices[j]
			j--
		}
		indices[j+1] = value
	}
}
