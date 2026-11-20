package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"codex_go/appserverdaemon"
	chatwidget "codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
)

// Rust parity: codex-rs/tui/src/app/remote_control.rs. The `/rc` command drives
// the local background server over its control socket, so this file is the host
// half of that command: verification, the calls themselves, and the preference
// the daemon persisted for its next restart.

const (
	// remoteControlTUIOperationTimeout bounds one `/rc` daemon call; the TUI
	// applies its own 30-second ceiling on top.
	remoteControlTUIOperationTimeout = 25 * time.Second
	// remoteControlMinimumDaemonVersion is the first daemon that serves
	// persistent remote control (Rust remote_control::verified_daemon_session).
	remoteControlMinimumDaemonVersion = "v0.163.0-alpha.2"
	// remoteControlUnverifiedDaemon mirrors Rust's wording when it cannot prove it
	// manages the background server.
	remoteControlUnverifiedDaemon = "Codex could not verify that it manages the local background server."
)

// remoteControlSettingsFile is where the daemon persists its remote control
// preference: <codex home>/app-server-daemon/settings.json sits two directories
// above the control socket (Rust remote_control::verified_daemon_session).
func remoteControlSettingsFile(socketPath string) string {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return ""
	}
	home := filepath.Dir(filepath.Dir(socketPath))
	if strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, "app-server-daemon", appserverdaemon.SettingsFileName)
}

// remoteControlSavedPreference reads the persisted preference. A missing or
// unreadable file reports nil, which the menu treats as unknown.
func remoteControlSavedPreference(socketPath string) *bool {
	path := remoteControlSettingsFile(socketPath)
	if path == "" {
		return nil
	}
	settings, err := appserverdaemon.LoadSettings(path)
	if err != nil || settings == nil {
		return nil
	}
	enabled := settings.RemoteControlEnabled
	return &enabled
}

// remoteControlDaemonSupported reports whether the daemon serves persistent
// remote control. A 0.0.0 development server is always accepted.
func remoteControlDaemonSupported(version string) bool {
	version = strings.TrimSpace(version)
	if version == "" || version == "0.0.0" {
		return true
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !semver.IsValid(version) {
		return true
	}
	return semver.Compare(version, remoteControlMinimumDaemonVersion) >= 0
}

// remoteControlServerRefusal reports whether an error is a daemon refusal the
// command words itself rather than a connection or verification failure.
func remoteControlServerRefusal(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	message := err.Error()
	switch message {
	case "remote control is disabled by managed requirements",
		"remote control requires ChatGPT authentication",
		"remote control requires ChatGPT authentication; API key auth is not supported":
		return message, true
	}
	return "", false
}

// remoteControlTUIRunner builds the `/rc` runner for a verified local daemon.
// A daemon older than persistent remote control is refused the way Rust refuses
// it, and the preference is read after every outcome so the menu can report an
// unsaved change.
func remoteControlTUIRunner(socketPath string, daemonVersion string) codextea.RemoteControlRunFunc {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return nil
	}
	return func(_ context.Context, action chatwidget.RemoteControlAction) (chatwidget.RemoteControlResponse, error) {
		if !remoteControlDaemonSupported(daemonVersion) {
			return chatwidget.RemoteControlResponse{}, errRemoteControlDaemonTooOld
		}
		response, err := runRemoteControlTUIRequest(socketPath, action)
		if err != nil {
			if message, refusal := remoteControlServerRefusal(err); refusal {
				response.Failed = true
				response.ServerMessage = message
				response.Saved = remoteControlSavedPreference(socketPath)
				return response, nil
			}
			return chatwidget.RemoteControlResponse{}, err
		}
		response.Saved = remoteControlSavedPreference(socketPath)
		return response, nil
	}
}

// runRemoteControlTUIRequest performs one control-socket call and reports the
// fields the command needs.
func runRemoteControlTUIRequest(socketPath string, action chatwidget.RemoteControlAction) (chatwidget.RemoteControlResponse, error) {
	switch action {
	case chatwidget.RemoteControlActionMenu:
		status, err := appserverdaemon.ReadRemoteControlStatusOnSocket(socketPath, remoteControlTUIOperationTimeout)
		if err != nil {
			return chatwidget.RemoteControlResponse{}, err
		}
		return chatwidget.RemoteControlResponse{Status: status.Status}, nil
	case chatwidget.RemoteControlActionEnable:
		status, err := appserverdaemon.EnableRemoteControlOnSocket(socketPath, remoteControlTUIOperationTimeout, 25*time.Millisecond)
		if err != nil {
			return chatwidget.RemoteControlResponse{}, err
		}
		return chatwidget.RemoteControlResponse{Status: status.Status}, nil
	case chatwidget.RemoteControlActionDisable:
		status, err := appserverdaemon.DisableRemoteControlOnSocket(socketPath, remoteControlTUIOperationTimeout, 25*time.Millisecond)
		if err != nil {
			return chatwidget.RemoteControlResponse{}, err
		}
		return chatwidget.RemoteControlResponse{Status: status.Status}, nil
	case chatwidget.RemoteControlActionAddDevice:
		pairing, err := appserverdaemon.StartRemoteControlPairingOnSocket(socketPath)
		if err != nil {
			return chatwidget.RemoteControlResponse{}, err
		}
		var code *string
		if pairing != nil && pairing.ManualPairingCode != nil {
			code = pairing.ManualPairingCode
		}
		return chatwidget.RemoteControlResponse{ManualCode: code}, nil
	default:
		return chatwidget.RemoteControlResponse{}, errRemoteControlUnknownAction
	}
}

// Both messages are shown verbatim by `/rc`.
var (
	// errRemoteControlDaemonTooOld matches Rust's guidance for a daemon that
	// cannot serve persistent remote control yet.
	errRemoteControlDaemonTooOld = errors.New("Update the background server with /daemon to use persistent remote control.")
	// errRemoteControlUnknownAction guards the runner against an action the
	// command cannot produce.
	errRemoteControlUnknownAction = errors.New("unknown remote control action")
)
