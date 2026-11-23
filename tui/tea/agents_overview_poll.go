package tea

import (
	"time"

	bubbletea "charm.land/bubbletea/v2"
)

// Rust #62858: the Agents overview can go stale when a thread changes from
// another client while this TUI holds no conversation stream for it. The
// visible dashboard therefore re-reads `thread/list` every 10 seconds, and the
// per-thread notifications still refresh it immediately.

// agentsOverviewPollInterval mirrors Rust's 10-second Cloud inventory poll.
// Tests shorten it to observe the tick without waiting.
var agentsOverviewPollInterval = 10 * time.Second

// agentsOverviewPollMsg signals one periodic inventory poll.
type agentsOverviewPollMsg struct{}

// agentsOverviewPollCmd schedules the next inventory poll while the dashboard
// is visible. Rust starts the interval only for a Cloud transport whose reply
// stream is not attached; the Go dashboard reads its inventory through
// `thread/list` for every backend, so it keeps polling for as long as the view
// is open. A model without an attached event loop (tests, embedders) has no
// timer to drive, so it schedules nothing.
func (m *Model) agentsOverviewPollCmd() bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil || !m.frames.attached() {
		return nil
	}
	return bubbletea.Tick(agentsOverviewPollInterval, func(time.Time) bubbletea.Msg {
		return agentsOverviewPollMsg{}
	})
}

// startAgentsOverviewPoll starts the polling chain once per dashboard open.
func (m *Model) startAgentsOverviewPoll() bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil || m.agentsOverviewPolling {
		return nil
	}
	cmd := m.agentsOverviewPollCmd()
	if cmd == nil {
		return nil
	}
	m.agentsOverviewPolling = true
	return cmd
}

// applyAgentsOverviewPoll refreshes the visible dashboard and re-arms the
// timer. A closed dashboard stops the chain because the tick is only
// rescheduled while the view exists.
func (m *Model) applyAgentsOverviewPoll() bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil {
		m.agentsOverviewPolling = false
		return nil
	}
	cmds := make([]bubbletea.Cmd, 0, 2)
	if refresh := m.refreshAgentsOverviewCmd(); refresh != nil {
		cmds = append(cmds, refresh)
	}
	cmds = appendAgentsOverviewCmd(cmds, m.agentsOverviewPollCmd())
	switch len(cmds) {
	case 0:
		return nil
	case 1:
		return cmds[0]
	default:
		return bubbletea.Batch(cmds...)
	}
}

// appendAgentsOverviewCmd appends a command that actually exists.
func appendAgentsOverviewCmd(cmds []bubbletea.Cmd, cmd bubbletea.Cmd) []bubbletea.Cmd {
	if cmd == nil {
		return cmds
	}
	return append(cmds, cmd)
}
