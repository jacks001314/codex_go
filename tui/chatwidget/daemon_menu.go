package chatwidget

import (
	"fmt"
	"strings"

	codextui "codex_go/tui"
)

// Local daemon maintenance records an update action only after explicit
// confirmation; the CLI executes it in the foreground after the TUI restores
// the terminal (Rust tui/src/app/daemon_menu.rs).

const (
	// DaemonMenuViewID identifies the `/daemon` source menu.
	DaemonMenuViewID = "daemon-menu"
	// DaemonConfirmViewID identifies the daemon update confirmation.
	DaemonConfirmViewID = "daemon-confirm"
	// DaemonRestartConfirmViewID identifies the managed restart confirmation.
	DaemonRestartConfirmViewID = "daemon-restart-confirm"

	// DaemonMenuItemPublicStable / DaemonMenuItemThisCli select a package source.
	DaemonMenuItemPublicStable = string(codextui.DaemonUpdateSourcePublicStable)
	DaemonMenuItemThisCli      = string(codextui.DaemonUpdateSourceThisCli)
	// DaemonMenuItemCancel / DaemonMenuItemUpdateAndExit are the confirmation's.
	DaemonMenuItemCancel        = "cancel"
	DaemonMenuItemUpdateAndExit = "update-and-exit"
	// DaemonMenuItemRestart / DaemonMenuItemRestartAndReconnect control restart.
	DaemonMenuItemRestart             = "restart"
	DaemonMenuItemRestartAndReconnect = "restart-and-reconnect"
)

const (
	daemonMenuStatusDisconnected      = "Not connected to the local background server."
	daemonMenuUnavailableRemote       = "Manage this server on its host. Local daemon updates are unavailable for remote connections."
	daemonMenuUnavailableNoCLI        = "Run the Codex CLI to manage the daemon from this menu."
	daemonMenuDisabledNoPackage       = "This CLI has no local package to copy"
	daemonMenuRestartUnverified       = "Codex hasn't verified it manages this server. Restart it where it was started."
	daemonMenuRestartUnavailable      = "Restart is available only for a managed local background server."
	daemonMenuPublicStableExplanation = "Install the latest public stable release. Restore production updates; keep your automatic-update setting."
	daemonMenuRestartNotice           = "\nThis may restart the daemon and interrupt active or queued work.\nCodex exits to update in this terminal. Relaunch it afterward."
)

// DaemonMenuConfig describes the session state the `/daemon` menu reports.
type DaemonMenuConfig struct {
	// LocalDaemon reports whether this session targets the shared local daemon.
	LocalDaemon bool
	// DaemonVersion is the running daemon's app-server version, when known.
	DaemonVersion string
	// Remote reports an explicit remote app server, which cannot be managed here.
	Remote bool
	// CLIExecutable is the launching CLI that can manage the daemon, or "" when
	// the running executable is not a Codex CLI.
	CLIExecutable string
	// HasPackage reports whether that CLI has a complete local package.
	HasPackage bool
	// ManagedDaemon reports whether Codex verified that it manages the local
	// background server and can restart it.
	ManagedDaemon bool
}

// DaemonMenuView builds the `/daemon` source menu (Rust
// App::open_daemon_menu).
func DaemonMenuView(config DaemonMenuConfig) SelectionView {
	status := daemonMenuStatusDisconnected
	if config.LocalDaemon {
		if version := strings.TrimSpace(config.DaemonVersion); version != "" {
			status = "Running daemon: " + version
		}
	}
	header := []string{"Daemon", status}
	unavailable := ""
	switch {
	case config.Remote:
		unavailable = daemonMenuUnavailableRemote
	case strings.TrimSpace(config.CLIExecutable) == "":
		unavailable = daemonMenuUnavailableNoCLI
	}
	if unavailable != "" {
		header = append(header, unavailable)
	}
	thisCliReason := ""
	if unavailable == "" && !config.HasPackage {
		thisCliReason = daemonMenuDisabledNoPackage
	}
	restartReason := ""
	switch {
	case unavailable != "":
		restartReason = daemonMenuRestartUnavailable
	case !config.LocalDaemon:
		restartReason = daemonMenuRestartUnavailable
	case !config.ManagedDaemon:
		restartReason = daemonMenuRestartUnverified
	}
	return SelectionView{
		ViewID:      DaemonMenuViewID,
		Title:       "Daemon",
		HeaderLines: header,
		AllowCancel: true,
		Items: []SelectionItem{
			{
				ID:              DaemonMenuItemPublicStable,
				Name:            "Install latest public stable",
				Disabled:        unavailable != "",
				DismissOnSelect: true,
			},
			{
				ID:              DaemonMenuItemThisCli,
				Name:            "Use this CLI build",
				Disabled:        unavailable != "" || thisCliReason != "",
				DisabledReason:  thisCliReason,
				DismissOnSelect: true,
			},
			{
				ID:              DaemonMenuItemRestart,
				Name:            "Restart background server",
				Disabled:        restartReason != "",
				DisabledReason:  restartReason,
				DismissOnSelect: true,
			},
		},
	}
}

// DaemonConfirmView builds the confirmation for a selected source (Rust
// App::confirm_daemon_update).
func DaemonConfirmView(source codextui.DaemonUpdateSource, cliVersion string, executable string) SelectionView {
	explanation := daemonMenuPublicStableExplanation
	if source == codextui.DaemonUpdateSourceThisCli {
		version := strings.TrimSpace(cliVersion)
		if version == "" {
			version = "unknown"
		}
		explanation = fmt.Sprintf(
			"Use this CLI package v%s from %s. Copy the complete package and pin it against automatic updates.",
			version,
			executable,
		)
	}
	return SelectionView{
		ViewID:      DaemonConfirmViewID,
		Title:       "Update daemon and exit Codex?",
		Subtitle:    explanation + daemonMenuRestartNotice,
		AllowCancel: true,
		Items: []SelectionItem{
			{ID: DaemonMenuItemCancel, Name: "Cancel", DismissOnSelect: true},
			{
				ID:                          DaemonMenuItemUpdateAndExit,
				Name:                        "Update and exit",
				RequireExplicitConfirmation: true,
				DismissOnSelect:             true,
			},
		},
	}
}

// DaemonRestartConfirmView builds the managed-server restart confirmation.
func DaemonRestartConfirmView() SelectionView {
	return SelectionView{
		ViewID:      DaemonRestartConfirmViewID,
		Title:       "Restart background server?",
		Subtitle:    "Active or queued work in other chats may be interrupted.\nCodex will reconnect automatically when the server is ready.",
		AllowCancel: true,
		Items: []SelectionItem{
			{ID: DaemonMenuItemCancel, Name: "Cancel", DismissOnSelect: true},
			{
				ID:              DaemonMenuItemRestartAndReconnect,
				Name:            "Restart and reconnect",
				DismissOnSelect: true,
			},
		},
	}
}
