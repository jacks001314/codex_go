package tea

import (
	"fmt"
	"strings"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
	tuistatus "codex_go/tui/status"
)

// Rust parity: codex-rs/tui/src/app/server_version_notice.rs and
// status::remote_connection::server_version_notice_for_tui (#43698).

// applyServerVersionNotice refreshes the retained startup warning and the
// command-center footer line. Disabling the preference clears both.
func (m *Model) applyServerVersionNotice() {
	if m == nil {
		return
	}
	if !m.showServerVersionNotice {
		m.clearServerVersionNotice()
		return
	}
	if m.State == nil || (!m.localDaemonSession && !m.remoteAppServer) {
		m.clearServerVersionNotice()
		return
	}
	client := strings.TrimSpace(m.State.CLIVersion)
	server := strings.TrimSpace(m.daemonVersion)
	if client == "" || server == "" {
		m.clearServerVersionNotice()
		return
	}
	message, ok := tuistatus.ServerVersionNoticeMessage(client, &server)
	if !ok {
		m.clearServerVersionNotice()
		return
	}
	if !m.serverVersionWarningAdded {
		m.warningCells = append(m.warningCells, historycell.NewServerVersionNotice(message, m.localDaemonSession))
		m.serverVersionWarningAdded = true
	}
	kind, _ := codextui.ServerVersionNoticeKindFor(client, server)
	comparison := "<"
	if kind == codextui.ServerVersionNoticeDifferent {
		comparison = "\u2260"
	}
	guidance := ""
	if m.localDaemonSession {
		guidance = " \u00b7 /daemon"
	}
	m.agentsOverviewServerVersionNotice = fmt.Sprintf(
		"Service v%s %s Codex CLI v%s%s",
		server,
		comparison,
		client,
		guidance,
	)
}

func (m *Model) clearServerVersionNotice() {
	if m == nil {
		return
	}
	filtered := m.warningCells[:0]
	for _, cell := range m.warningCells {
		switch warning := cell.(type) {
		case historycell.WarningEventCell:
			if warning.ServerVersionNotice {
				continue
			}
		case *historycell.WarningEventCell:
			if warning != nil && warning.ServerVersionNotice {
				continue
			}
		}
		filtered = append(filtered, cell)
	}
	m.warningCells = filtered
	m.serverVersionWarningAdded = false
	m.agentsOverviewServerVersionNotice = ""
}
