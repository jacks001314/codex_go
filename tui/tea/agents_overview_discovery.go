package tea

import bubbletea "github.com/charmbracelet/bubbletea"

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
		return m.refreshAgentsOverviewCmd()
	}
	return nil
}
