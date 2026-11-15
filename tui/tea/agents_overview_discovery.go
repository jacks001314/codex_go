package tea

import bubbletea "charm.land/bubbletea/v2"

// History pagination for the command center (Rust #49106): the "Show more" row
// publishes the next page of retained tasks and refreshes their metadata.

// showMoreAgentsOverviewCmd publishes the next retained page. A published page
// triggers a refresh so the newly visible tasks get current status and details,
// matching Rust's demand-driven discovery refresh.
func (m *Model) showMoreAgentsOverviewCmd() bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil {
		return nil
	}
	added, _ := m.agentsOverview.NextBatch()
	if added > 0 {
		// Rust #61596: a "Show more" request without pending refills belongs to
		// the backend that asked for it, so its next page cannot be attributed
		// to another backend's budget.
		if state := m.agentsOverviewBackendState(m.backendID); state != nil && state.refill == 0 {
			state.showMore = true
		}
		return m.refreshAgentsOverviewCmd()
	}
	return nil
}
