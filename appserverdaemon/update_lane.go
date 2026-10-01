package appserverdaemon

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"codex_go/install"
)

// Shared update-lane pieces (Rust app-server-daemon/src/update_loop.rs and
// manual_update.rs): what authorized an update run, how a selection is read, and
// the messages the CLI reports back.

// UpdateTriggerKind mirrors Rust UpdateTrigger: a scheduled check may take the
// production channel, a manual request is an operator asking for the latest
// release, and a production restore is the single-use authority a CLI launch
// under the operation lock hands to the updater to undo a pin.
type UpdateTriggerKind int

const (
	UpdateTriggerScheduled UpdateTriggerKind = iota
	UpdateTriggerManual
	UpdateTriggerRestoreProduction
)

// UpdateTrigger carries what authorized one update run.
type UpdateTrigger struct {
	Kind UpdateTriggerKind
	// Release is the pinned release a production restore was authorized for.
	Release string
}

// The manual update messages (Rust manual_update::run).
const (
	ManualUpdateRestartedMessage  = "The managed installation is ready and the running daemon was restarted. Active or queued work may have been interrupted."
	ManualUpdateCurrentMessage    = "The managed installation and running daemon are already current; the daemon was left running."
	ManualUpdateNotRunningMessage = "The managed installation is ready; no daemon was running when the updater checked."
	// ManualUpdateUnsupportedMessage mirrors Rust manual_update::UNSUPPORTED_MESSAGE.
	ManualUpdateUnsupportedMessage = "This command requires a daemon package selected from its managed releases directory."
)

// manualUpdateSocketPath is where the running updater accepts manual requests
// (Rust Daemon::manual_update_socket_path).
func manualUpdateSocketPath(daemon *Daemon) string {
	if daemon == nil || daemon.Paths == nil {
		return ""
	}
	return pidPathWithExtension(daemon.Paths.UpdatePIDFile, "sock")
}

// selectedRelease mirrors Rust update_loop::selected_release: the package root,
// the resolved release directory, and its name.
func selectedRelease(daemon *Daemon) (root string, release string, name string, err error) {
	home := daemonCodexHome(daemon)
	if home == "" {
		return "", "", "", ErrDaemonPathsRequired
	}
	root = PackageRoot(home)
	release = canonicalPath(filepath.Join(root, "current"))
	if release == "" {
		return "", "", "", fmt.Errorf("failed to resolve the selected daemon release %s", filepath.Join(root, "current"))
	}
	return root, release, filepath.Base(release), nil
}

// packageRootCodexHome resolves the Codex home that owns a package root, which
// is what an installer run receives as CODEX_HOME.
func packageRootCodexHome(root string) string {
	return filepath.Dir(filepath.Dir(root))
}

// releaseSelectionUnstable mirrors Rust release_selection_unstable: a scheduled
// run waits for an installer that is between publishing `current` and its
// latest-channel marker, while a manual or restoring run refuses to continue.
func releaseSelectionUnstable(daemon *Daemon, trigger UpdateTrigger) (bool, error) {
	home := daemonCodexHome(daemon)
	if isStableStandaloneRelease(home, ManagedCodexBin(home)) {
		return false, nil
	}
	if trigger.Kind == UpdateTriggerRestoreProduction {
		supported, err := manualUpdateSupported(daemon)
		if err != nil {
			return false, err
		}
		if supported {
			return false, nil
		}
	}
	if trigger.Kind != UpdateTriggerScheduled {
		return false, errors.New("standalone install selection changed during the update")
	}
	return true, nil
}

// manualUpdateSupported mirrors Rust manual_update::supported: either the
// selected release follows the production channel, or the selection is a
// complete package inside the daemon's own releases directory (a pin).
func manualUpdateSupported(daemon *Daemon) (bool, error) {
	home := daemonCodexHome(daemon)
	managedBin := ManagedCodexBin(home)
	if isStableStandaloneRelease(home, managedBin) {
		return true, nil
	}
	root, release, _, err := selectedRelease(daemon)
	if err != nil {
		return false, nil
	}
	releases := canonicalPath(filepath.Join(root, releasesDirName))
	if releases == "" || filepath.Dir(release) != releases {
		return false, nil
	}
	if !isRegularFile(managedBin) {
		return false, nil
	}
	return pathWithin(canonicalPath(managedBin), release), nil
}

// unsupportedUpdateOutput mirrors Rust manual_update::unsupported.
func unsupportedUpdateOutput(daemon *Daemon) *UpdateOutput {
	managedCodexPath := ManagedCodexBin(daemonCodexHome(daemon))
	return &UpdateOutput{
		Status:           UpdateUnsupported,
		ManagedCodexPath: managedCodexPath,
		InstalledVersion: managedCodexVersionBestEffort(managedCodexPath),
		RunningVersion:   runningAppServerVersion(daemon),
		Message:          ManualUpdateUnsupportedMessage,
	}
}

func runningAppServerVersion(daemon *Daemon) *string {
	if daemon == nil || daemon.Paths == nil {
		return nil
	}
	version, err := ProbeAppServerVersionOnSocket(daemon.Paths.SocketPath, ControlSocketProbeTimeout)
	if err != nil {
		return nil
	}
	return &version
}

// nextUpdateDelay mirrors Rust next_update_delay: the configured interval, or
// "stop" once automatic updates are disabled.
func nextUpdateDelay(daemon *Daemon) (time.Duration, bool) {
	settings, err := daemon.LoadSettings()
	if err != nil {
		return time.Minute, true
	}
	if !settings.AutoUpdateEnabled() {
		return 0, false
	}
	return settings.UpdateInterval(), true
}

// restartModeForTrigger mirrors Rust update_once's restart-mode selection.
func restartModeForTrigger(trigger UpdateTrigger, releaseChanged bool, managedIdentity install.ExecutableIdentity, runningUpdaterIdentity install.ExecutableIdentity) RestartMode {
	switch trigger.Kind {
	case UpdateTriggerManual, UpdateTriggerRestoreProduction:
		if releaseChanged {
			// A package can carry different resources even when the CLI binary is
			// identical, so a release change always replaces the running process.
			return RestartAlways
		}
		return RestartIfBinaryOrVersionChanged
	default:
		if managedIdentity != runningUpdaterIdentity {
			return RestartAlways
		}
		return RestartIfVersionChanged
	}
}
