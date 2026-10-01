package tea

import (
	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// Local daemon maintenance records an update action only after explicit
// confirmation; the CLI runs it in the foreground once the TUI exits
// (Rust tui/src/app/daemon_menu.rs + event_dispatch::RunDaemonUpdate).

// openDaemonMenu shows the `/daemon` source menu.
func (m *Model) openDaemonMenu() {
	if m == nil {
		return
	}
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.DaemonMenuView(chatwidget.DaemonMenuConfig{
		LocalDaemon:   m.localDaemonSession,
		DaemonVersion: m.daemonVersion,
		Remote:        m.remoteAppServer,
		CLIExecutable: m.daemonCLIExecutable,
		HasPackage:    m.daemonCLIPackage,
	}))
}

// confirmDaemonUpdate shows the confirmation for a selected source.
func (m *Model) confirmDaemonUpdate(source codextui.DaemonUpdateSource) bubbletea.Cmd {
	if m == nil || m.daemonCLIExecutable == "" || m.remoteAppServer {
		return nil
	}
	m.daemonConfirmSource = source
	m.openSelectionViewModal(ModalKindGeneric, chatwidget.DaemonConfirmView(source, m.daemonCLIVersion, m.daemonCLIExecutable))
	return nil
}

// runDaemonUpdate records the update the CLI should perform after this TUI
// exits, then exits immediately (Rust AppEvent::RunDaemonUpdate).
func (m *Model) runDaemonUpdate(source codextui.DaemonUpdateSource) bubbletea.Cmd {
	if m == nil {
		return bubbletea.Quit
	}
	m.pendingUpdateAction = codextui.DaemonUpdateAction(source)
	return bubbletea.Quit
}

// PendingUpdateAction reports the update the TUI asked the CLI to run, or "".
func (m *Model) PendingUpdateAction() codextui.UpdateAction {
	if m == nil {
		return ""
	}
	return m.pendingUpdateAction
}
