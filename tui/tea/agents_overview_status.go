package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	agentsoverview "codex_go/tui/agents_overview"
)

// Rust parity: codex-rs/tui/src/app/agents_overview.rs (#62858). The Agents
// overview can go stale when another client changes a Cloud thread, so a
// thread/status/changed notification patches the listed row and the picker
// entry without waiting for the next list refresh.
func (m *Model) applyAgentThreadStatus(msg AgentThreadStatusMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID := strings.TrimSpace(msg.ThreadID)
	if threadID == "" {
		return nil
	}
	if m.agentsOverview != nil {
		if m.agentsOverview.SetRowStatus(threadID, msg.StatusType, msg.Active, msg.WaitingOnApproval, msg.WaitingOnUserInput) {
			if known, ok := m.agentsOverviewKnownRows[threadID]; ok {
				known.StatusType = strings.TrimSpace(msg.StatusType)
				known.StatusActive = msg.Active
				known.WaitingOnApproval = msg.WaitingOnApproval
				known.WaitingOnUserInput = msg.WaitingOnUserInput
				known.Group = agentsoverview.GroupForStatus(known.StatusType, msg.WaitingOnApproval, msg.WaitingOnUserInput)
				m.agentsOverviewKnownRows[threadID] = known
			}
		}
	}
	for index := range m.agentItems {
		if strings.TrimSpace(m.agentItems[index].ThreadID) != threadID {
			continue
		}
		m.agentItems[index].IsRunning = msg.Active
	}
	return nil
}
