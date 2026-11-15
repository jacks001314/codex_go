package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

// Rust #61596: the command center tracks queued refreshes, "Show more"
// requests, refill counts, failures, and availability per backend. Go's TUI
// owns one live connection at a time, so a refresh for a backend that is no
// longer displayed cannot run immediately; it stays queued until that backend
// is displayed again. A late page that belongs to a cancelled refresh is
// discarded instead of consuming another backend's budget.
type agentsOverviewBackendRefresh struct {
	// inflight is set on the model-wide slot while this backend refreshes.
	inflight bool
	// pending keeps one coalesced refresh request per backend.
	pending bool
	// showMore records the "Show more" request until a refresh consumes it.
	showMore bool
	// refill counts visible vacancies left by removals in this backend.
	refill int
	// failed records the last refresh result for this backend.
	failed bool
	// disconnected clears "Show more" while the backend has no connection.
	disconnected bool
	// hasMore is the discovery cursor state last observed for this backend.
	hasMore bool
}

// agentsOverviewBackendState returns the refresh bookkeeping for one backend,
// creating it on first use.
func (m *Model) agentsOverviewBackendState(backendID codextui.BackendID) *agentsOverviewBackendRefresh {
	if m == nil {
		return nil
	}
	if m.agentsOverviewBackends == nil {
		m.agentsOverviewBackends = map[codextui.BackendID]*agentsOverviewBackendRefresh{}
	}
	state := m.agentsOverviewBackends[backendID]
	if state == nil {
		state = &agentsOverviewBackendRefresh{}
		m.agentsOverviewBackends[backendID] = state
	}
	return state
}

// agentsOverviewBackendPeek returns the bookkeeping for one backend without
// creating an entry.
func (m *Model) agentsOverviewBackendPeek(backendID codextui.BackendID) *agentsOverviewBackendRefresh {
	if m == nil {
		return nil
	}
	return m.agentsOverviewBackends[backendID]
}

// syncAgentsOverviewBackendView publishes the displayed backend's pagination
// and failure state after a backend switch, a completed refresh, or a
// disconnect (Rust #61596 backend_selection / cancel_agents_overview_refresh).
func (m *Model) syncAgentsOverviewBackendView() {
	if m == nil || m.agentsOverview == nil {
		return
	}
	state := m.agentsOverviewBackendPeek(m.backendID)
	hasMore := state != nil && !state.disconnected && state.hasMore
	m.agentsOverview.SetHasMore(hasMore)
	m.agentsOverview.SetShowMoreFailed(state != nil && state.failed)
}

// cancelAgentsOverviewRefresh discards an in-flight read for backendID so a
// page started before a mutation, event lag, or disconnect cannot apply later
// (Rust #61596 cancel_agents_overview_refresh). A pending "Show more" request
// is preserved so the next refresh still pages the owning backend.
func (m *Model) cancelAgentsOverviewRefresh(backendID codextui.BackendID) {
	if m == nil {
		return
	}
	if m.agentsOverviewInflight && m.agentsOverviewInflightFor == backendID {
		m.agentsOverviewInflight = false
		m.agentsOverviewInflightFor = codextui.BackendID{}
		// Invalidate any result that is already on its way back.
		m.agentsOverviewRefresh++
		if state := m.agentsOverviewBackendPeek(backendID); state != nil && state.showMore {
			state.showMore = true
		}
		if m.agentsOverview != nil {
			m.agentsOverview.SetShowMoreLoading(false)
		}
		m.agentsOverviewPending = false
	}
	state := m.agentsOverviewBackendPeek(backendID)
	if state == nil {
		m.syncAgentsOverviewBackendView()
		return
	}
	state.pending = false
	if state.disconnected {
		state.failed = false
	}
	m.syncAgentsOverviewBackendView()
}

// drainAgentsOverviewRefreshQueue starts the displayed backend's queued
// refresh when the shared slot is free (Rust #61596
// drain_agents_overview_refresh_queue).
func (m *Model) drainAgentsOverviewRefreshQueue() bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil || m.agentsOverviewInflight {
		return nil
	}
	state := m.agentsOverviewBackendPeek(m.backendID)
	if state == nil || !state.pending || state.disconnected {
		return nil
	}
	state.pending = false
	return m.refreshAgentsOverviewCmd()
}

// noteAgentsOverviewRemovedThread counts the visible vacancy an archive/delete
// leaves in the owning backend so the next page fills it without expanding the
// window (Rust #61596 refill_by_backend).
func (m *Model) noteAgentsOverviewRemovedThread(threadID string) {
	if m == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || m.agentsOverviewRemoved[threadID] {
		return
	}
	row, known := m.agentsOverviewKnownRows[threadID]
	if !known || row.IsSubagent {
		return
	}
	if _, hidden := m.agentsOverviewHidden[threadID]; hidden {
		return
	}
	owner := m.backendID
	if recorded, ok := m.threadBackendOwner(threadID); ok {
		owner = recorded
	}
	state := m.agentsOverviewBackendState(owner)
	if state == nil {
		return
	}
	state.refill++
}

// countNewAgentsOverviewRoots reports roots that the refresh adds to the
// retained membership; each fills one vacancy in refreshBackend.
func (m *Model) countNewAgentsOverviewRoots(rows []agentsoverview.Row) int {
	if m == nil {
		return 0
	}
	count := 0
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		threadID := strings.TrimSpace(row.ThreadID)
		if threadID == "" || row.IsSubagent {
			continue
		}
		if _, duplicate := seen[threadID]; duplicate {
			continue
		}
		seen[threadID] = struct{}{}
		if m.agentsOverviewRemoved[threadID] {
			continue
		}
		if _, hidden := m.agentsOverviewHidden[threadID]; hidden {
			continue
		}
		if _, known := m.agentsOverviewKnownRows[threadID]; known {
			continue
		}
		count++
	}
	return count
}

// restoreAgentsOverviewRemovedThread consumes the vacancy a previously removed
// task left behind when that task becomes visible again, matching Rust's
// threads-map removal and re-add.
func (m *Model) restoreAgentsOverviewRemovedThread(threadID string) {
	if m == nil || !m.agentsOverviewRemoved[strings.TrimSpace(threadID)] {
		return
	}
	threadID = strings.TrimSpace(threadID)
	owner := m.backendID
	if recorded, ok := m.threadBackendOwner(threadID); ok {
		owner = recorded
	}
	state := m.agentsOverviewBackendPeek(owner)
	if state != nil && state.refill > 0 {
		state.refill--
	}
}
