package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codex_go/codexuds"
	"codex_go/envutil"
	"codex_go/features"
	"codex_go/install"
)

type LifecycleRunner struct {
	Daemon *Daemon
	Now    func() time.Time
}

const (
	// OperationLockTimeout leaves room for the longest graceful stop plus the
	// forced-exit check and a restart (Rust #43572).
	OperationLockTimeout = time.Duration(MaxShutdownGraceSeconds+75) * time.Second
	OperationLockRetry   = 50 * time.Millisecond
)

func NewLifecycleRunner(daemon *Daemon) *LifecycleRunner {
	return &LifecycleRunner{Daemon: daemon, Now: time.Now}
}

func NewLifecycleRunnerForCodexHome(codexHome string, cliVersion string) *LifecycleRunner {
	return NewLifecycleRunner(NewDaemonForCodexHome(codexHome, cliVersion))
}

var (
	probeAppServerVersionOnSocket = ProbeAppServerVersionOnSocket
	enableRemoteControlOnSocket   = EnableRemoteControlOnSocket
	disableRemoteControlOnSocket  = DisableRemoteControlOnSocket
	ensureManagedCodexBin         = EnsureManagedCodexBin
	prepareManagedDaemon          = prepareDaemonInstall
	pidBackendIsStartingOrRunning = func(backend *PIDBackend) (bool, error) { return backend.IsStartingOrRunning() }
	startPIDBackend               = func(backend *PIDBackend) (*uint32, error) { return backend.Start() }
	stopPIDBackend                = func(backend *PIDBackend, graceSeconds int) error { return backend.StopWithGrace(graceSeconds) }
	lifecycleReadyTimeout         = RemoteControlReadyTimeout
	lifecycleReadyRetry           = 50 * time.Millisecond
	readStderrLogTail             = ReadStderrLogTail
	// managedCodexVersionBestEffort is injectable so the package checks can pin
	// the version of a staged executable without running it.
	managedCodexVersionBestEffort = ManagedCodexVersionBestEffort
	// ensureDetachedLaunchProbe is injectable so the updater's restart preflight
	// can be pinned without a runnable managed binary.
	ensureDetachedLaunchProbe = ensureDetachedLaunch
)

func (r *LifecycleRunner) Run(command LifecycleCommand) (*LifecycleOutput, error) {
	if r == nil {
		r = &LifecycleRunner{}
	}
	// Only start/restart perform the Windows elevation handoff the daemon
	// forbids; stop and version must keep working (Rust lib.rs run(command)).
	if command == LifecycleStart || command == LifecycleRestart {
		if err := EnsureNonElevated(); err != nil {
			return nil, err
		}
	}
	daemon := r.daemon()
	backend := BackendPID
	switch command {
	case LifecycleStart:
		lock, err := r.acquireOperationLock()
		if err != nil {
			return nil, err
		}
		defer lock.Close()
		// Every lifecycle command re-resolves the installation it owns: an
		// update may have migrated the package while this command waited for the
		// lock (Rust Daemon::run -> current_installation).
		daemon.refreshInstallation()
		settings, err := daemon.LoadSettings()
		if err != nil {
			return nil, err
		}
		return r.start(settings)
	case LifecycleRestart:
		lock, err := r.acquireOperationLock()
		if err != nil {
			return nil, err
		}
		defer lock.Close()
		daemon.refreshInstallation()
		settings, err := daemon.LoadSettings()
		if err != nil {
			return nil, err
		}
		return r.restart(settings, &backend)
	case LifecycleStop:
		lock, err := r.acquireOperationLock()
		if err != nil {
			return nil, err
		}
		defer lock.Close()
		daemon.refreshInstallation()
		// Stop must work even when settings are unreadable or partially edited.
		output, err := r.stop(daemon.LoadSettingsForStop())
		if err != nil {
			return nil, err
		}
		// A stop retires the running app-server's saved threads (Rust
		// run(LifecycleCommand::Stop)).
		if recoveryErr := discardPendingRecovery(daemon); recoveryErr != nil {
			daemon.Diagnostic("warning: failed to clear saved threads after daemon stop: %v", recoveryErr)
		}
		return output, nil
	case LifecycleVersion:
		settings, err := daemon.LoadSettings()
		if err != nil {
			return nil, err
		}
		return r.version(settings)
	default:
		_, err := ParseLifecycleCommand(string(command))
		return nil, err
	}
}

func (r *LifecycleRunner) Bootstrap(options *BootstrapOptions) (*BootstrapOutput, error) {
	if err := EnsureNonElevated(); err != nil {
		return nil, err
	}
	lock, err := r.acquireOperationLock()
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	return r.bootstrapLocked(options)
}

// StartWithFeatures starts the managed app-server, merging the stored feature
// overrides with the launch's own (Rust app-server-daemon::start_with_features).
// Startup diagnostics stay out of the caller's terminal because a live TUI owns
// stderr.
func (r *LifecycleRunner) StartWithFeatures(features map[string]bool) (*LifecycleOutput, error) {
	if err := EnsureNonElevated(); err != nil {
		return nil, err
	}
	daemon := r.daemon()
	daemon.LogDiagnostics = true
	lock, err := r.acquireOperationLock()
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	daemon.refreshInstallation()
	settings, err := daemon.LoadSettings()
	if err != nil {
		return nil, err
	}
	merged := CloneFeatureOverrides(settings.FeatureOverrides)
	if merged == nil {
		merged = map[string]bool{}
	}
	for name, enabled := range features {
		merged[name] = enabled
	}
	return r.startWithFeatureOverrides(settings, merged)
}

// RestartWithFeatures applies confirmed feature settings to an existing managed
// daemon, restarting it only when the settings actually change
// (Rust app-server-daemon::restart_with_features).
func (r *LifecycleRunner) RestartWithFeatures(features map[string]bool) (*LifecycleOutput, error) {
	if err := EnsureNonElevated(); err != nil {
		return nil, err
	}
	daemon := r.daemon()
	lock, err := r.acquireOperationLock()
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	daemon.refreshInstallation()
	settings, err := daemon.LoadSettings()
	if err != nil {
		return nil, err
	}
	backend, err := r.runningAppServerBackend(settings)
	if err != nil {
		return nil, err
	}
	if backend == nil {
		return nil, errors.New("no running managed daemon; rerun the command to check the current server")
	}
	previous := CloneFeatureOverrides(settings.FeatureOverrides)
	merged := CloneFeatureOverrides(settings.FeatureOverrides)
	if merged == nil {
		merged = map[string]bool{}
	}
	for name, enabled := range features {
		merged[name] = enabled
	}
	if FeatureOverridesEqual(previous, merged) {
		return r.startWithFeatureOverrides(settings, previous)
	}
	settings.FeatureOverrides = CloneFeatureOverrides(merged)
	backendKind := BackendPID
	return r.restart(settings, &backendKind)
}

// RestartApplyingFeatures applies an explicit restart's confirmed feature
// toggles, replaces saved alias spellings, and always restarts the managed
// backend. Rust #53751 uses the same path for running and stopped daemons.
func (r *LifecycleRunner) RestartApplyingFeatures(featureOverrides map[string]bool) (*LifecycleOutput, error) {
	if err := EnsureNonElevated(); err != nil {
		return nil, err
	}
	daemon := r.daemon()
	lock, err := r.acquireOperationLock()
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	daemon.refreshInstallation()
	settings, err := daemon.LoadSettings()
	if err != nil {
		return nil, err
	}
	merged := CloneFeatureOverrides(settings.FeatureOverrides)
	if merged == nil {
		merged = map[string]bool{}
	}
	for name, enabled := range featureOverrides {
		canonical, ok := features.CanonicalKey(name)
		if !ok {
			return nil, fmt.Errorf("daemon restart requires a boolean feature toggle: %s", name)
		}
		for saved := range merged {
			savedCanonical, ok := features.CanonicalKey(saved)
			if ok && savedCanonical == canonical {
				delete(merged, saved)
			}
		}
		merged[canonical] = enabled
	}
	settings.FeatureOverrides = CloneFeatureOverrides(merged)
	backendKind := BackendPID
	return r.restart(settings, &backendKind)
}

func (r *LifecycleRunner) bootstrapLocked(options *BootstrapOptions) (*BootstrapOutput, error) {
	daemon := r.daemon()
	// Bootstrap keeps stored updater preferences, feature overrides, and the
	// shutdown grace period; only the launch's remote-control setting changes
	// (Rust #43542).
	settings, err := daemon.LoadSettings()
	if err != nil {
		return nil, err
	}
	if options != nil {
		settings.RemoteControlEnabled = options.RemoteControlEnabled
	} else {
		settings.RemoteControlEnabled = false
	}
	socketPath := daemonSocketPath(daemon)
	if socketPath != "" {
		if _, err := probeAppServerVersionOnSocket(socketPath, ControlSocketProbeTimeout); err == nil {
			backend, backendErr := r.runningAppServerBackend(settings)
			if backendErr != nil {
				return nil, backendErr
			}
			if backend == nil {
				return nil, errors.New("app server is running but is not managed by codex app-server daemon")
			}
		}
	}
	// A fresh launch installs the daemon's own package before it checks the
	// managed executable (Rust bootstrap_locked).
	if err := prepareManagedDaemon(daemon, settings); err != nil {
		return nil, err
	}
	daemon.refreshInstallation()
	if err := ensureManagedCodexBin(daemon.Paths.ManagedCodexBin); err != nil {
		return nil, err
	}
	if err := daemon.SaveSettings(settings); err != nil {
		return nil, err
	}
	// Stop an existing updater before replacing the app-server so it cannot
	// swap the selected package mid-bootstrap (Rust #43542).
	if err := stopPIDBackend(r.updateLoopBackend(settings), settings.ShutdownGraceSecondsValue()); err != nil {
		return nil, err
	}
	if running, err := pidBackendIsStartingOrRunning(r.appServerBackend(settings)); err != nil {
		return nil, err
	} else if running {
		if err := discardPendingRecovery(daemon); err != nil {
			daemon.Diagnostic("warning: failed to clear stale daemon recovery before bootstrap: %v", err)
		}
		if err := stopPIDBackend(r.appServerBackend(settings), settings.ShutdownGraceSecondsValue()); err != nil {
			return nil, err
		}
	}
	if _, err := startPIDBackend(r.appServerBackend(settings)); err != nil {
		return nil, err
	}
	autoUpdateEnabled, err := ensureManagedUpdater(daemon, settings)
	if err != nil {
		return nil, err
	}
	if autoUpdateEnabled && supportsDaemonCommand(ManagedCodexBin(daemonCodexHome(daemon)), []string{"update", "--help"}) {
		// Rust #53845: a supported updater must publish its manual-request
		// socket before bootstrap reports success. Older updaters omit the
		// command and retain their previous compatibility behavior.
		if err := waitForManagedUpdaterSocketReady(
			r.updateLoopBackend(settings),
			manualUpdateSocketPath(daemon),
			pidBackendIsStartingOrRunning,
		); err != nil {
			context := err.Error()
			if tail, tailErr := ReadStderrLogTail(daemon.Paths.UpdatePIDFile); tailErr == nil && tail != nil {
				tail.AppendToContext(&context)
			}
			return nil, errors.New(context)
		}
	}
	appServerVersion, err := r.waitUntilReady()
	if err != nil {
		return nil, err
	}
	managedVersion := r.managedVersion()
	output := daemon.BootstrapOutput(settings, appServerVersion, managedVersion)
	output.AutoUpdateEnabled = autoUpdateEnabled
	return output, nil
}

func (r *LifecycleRunner) EnsureRemoteControlStarted() (*RemoteControlStartOutput, error) {
	if err := EnsureNonElevated(); err != nil {
		return nil, err
	}
	daemon := r.daemon()
	lock, err := r.acquireOperationLock()
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	bootstrapped, err := r.IsBootstrapped()
	if err != nil {
		return nil, err
	}
	if bootstrapped {
		if _, err := r.setRemoteControlLocked(RemoteControlEnabled); err != nil {
			return nil, err
		}
		settings, err := daemon.LoadSettings()
		if err != nil {
			return nil, err
		}
		output, err := r.start(settings)
		if err != nil {
			return nil, err
		}
		return daemon.RemoteControlStartFromLifecycle(output), nil
	}
	output, err := r.bootstrapLocked(&BootstrapOptions{RemoteControlEnabled: true})
	if err != nil {
		return nil, err
	}
	return daemon.RemoteControlStartFromBootstrap(output), nil
}

func (r *LifecycleRunner) IsBootstrapped() (bool, error) {
	daemon := r.daemon()
	if daemon == nil || daemon.Paths == nil || strings.TrimSpace(daemon.Paths.SettingsFile) == "" {
		return false, nil
	}
	info, err := os.Stat(daemon.Paths.SettingsFile)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, fmt.Errorf("daemon settings path is a directory: %s", daemon.Paths.SettingsFile)
	}
	if _, err := daemon.LoadSettings(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *LifecycleRunner) SetRemoteControl(mode RemoteControlMode) (*RemoteControlOutput, error) {
	if err := EnsureNonElevated(); err != nil {
		return nil, err
	}
	lock, err := r.acquireOperationLock()
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	return r.setRemoteControlLocked(mode)
}

func (r *LifecycleRunner) setRemoteControlLocked(mode RemoteControlMode) (*RemoteControlOutput, error) {
	daemon := r.daemon()
	settings, err := daemon.LoadSettings()
	if err != nil {
		return nil, err
	}
	previous := settings.RemoteControlEnabled
	enabled := mode.Enabled()
	status := RemoteControlStatusForMode(mode)
	backend, err := r.runningAppServerBackend(settings)
	if err != nil {
		return nil, err
	}
	socketPath := daemonSocketPath(daemon)
	if backend == nil && socketPath != "" {
		if _, err := probeAppServerVersionOnSocket(socketPath, ControlSocketProbeTimeout); err == nil {
			return nil, errors.New("app server is running but is not managed by codex app-server daemon")
		}
	}
	if previous == enabled {
		var appServerVersion *string
		if backend != nil {
			version, err := probeAppServerVersionOnSocket(socketPath, RemoteControlReadyTimeout)
			if err != nil {
				return nil, err
			}
			appServerVersion = &version
			switch mode {
			case RemoteControlEnabled:
				if _, err := enableRemoteControlOnSocket(socketPath, RemoteControlReadyTimeout, 50*time.Millisecond); err != nil {
					return nil, err
				}
			case RemoteControlDisabled:
				if _, err := disableRemoteControlOnSocket(socketPath, RemoteControlReadyTimeout, 50*time.Millisecond); err != nil {
					return nil, err
				}
			}
		}
		return daemon.RemoteControlOutput(AlreadyRemoteControlStatusForMode(mode), backend, enabled, appServerVersion), nil
	}
	settings.RemoteControlEnabled = enabled
	if err := daemon.SaveSettings(settings); err != nil {
		return nil, err
	}
	var appServerVersion *string
	if backend != nil {
		if err := ensureManagedCodexBin(daemon.Paths.ManagedCodexBin); err != nil {
			return nil, err
		}
		if err := discardPendingRecovery(daemon); err != nil {
			daemon.Diagnostic("warning: failed to clear stale recovery before remote-control restart: %v", err)
		}
		if err := stopPIDBackend(r.appServerBackend(&DaemonSettings{RemoteControlEnabled: previous}), settings.ShutdownGraceSecondsValue()); err != nil {
			return nil, err
		}
		if _, err := startPIDBackend(r.appServerBackend(settings)); err != nil {
			return nil, err
		}
		version, err := r.waitUntilReady()
		if err != nil {
			return nil, err
		}
		appServerVersion = &version
		// The restarted server keeps the saved updater preference (Rust
		// Daemon::set_remote_control_locked).
		if _, err := ensureManagedUpdater(daemon, settings); err != nil {
			daemon.Diagnostic("warning: failed to ensure managed updater after remote-control restart: %v", err)
		}
	}
	return daemon.RemoteControlOutput(status, backendFromVersion(appServerVersion), enabled, appServerVersion), nil
}

func daemonSocketPath(daemon *Daemon) string {
	if daemon == nil || daemon.Paths == nil {
		return ""
	}
	return strings.TrimSpace(daemon.Paths.SocketPath)
}

func (r *LifecycleRunner) runningAppServerBackend(settings *DaemonSettings) (*BackendKind, error) {
	running, err := pidBackendIsStartingOrRunning(r.appServerBackend(settings))
	if err != nil {
		return nil, err
	}
	if !running {
		return nil, nil
	}
	backend := BackendPID
	return &backend, nil
}

func (r *LifecycleRunner) TryRestartIfRunning(mode RestartMode, updaterRefreshMode UpdaterRefreshMode, managedCodexBin string) (RestartIfRunningOutcome, error) {
	lock, acquired, err := r.tryAcquireOperationLock()
	if err != nil {
		return "", err
	}
	if !acquired {
		return RestartBusy, nil
	}
	defer lock.Close()
	daemon := r.daemon()
	settings, err := daemon.LoadSettings()
	if err != nil {
		return "", err
	}
	backend, err := r.runningAppServerBackend(settings)
	if err != nil {
		return "", err
	}
	socketPath := daemonSocketPath(daemon)
	if backend == nil {
		if socketPath != "" {
			if _, err := probeAppServerVersionOnSocket(socketPath, ControlSocketProbeTimeout); err == nil {
				return "", errors.New("app server is running but is not managed by codex app-server daemon")
			}
		}
		return RestartNotRunning, nil
	}
	// The installer can retarget `current` while this update waits for the lock
	// or probes the running server. Never restart from a release that is no
	// longer the selected latest-channel binary (Rust try_restart_if_running).
	if !isStableStandaloneRelease(daemonCodexHome(daemon), ManagedCodexBin(daemonCodexHome(daemon))) ||
		canonicalPath(ManagedCodexBin(daemonCodexHome(daemon))) != canonicalPath(managedCodexBin) {
		return RestartAlreadyCurrent, nil
	}
	if mode == RestartIfBinaryOrVersionChanged {
		managedIdentity, err := install.ExecutableIdentityFromFile(managedCodexBin)
		if err != nil {
			return "", err
		}
		runningIdentity, err := NewPIDBackend(daemon.BackendPaths(settings)).RunningExecutableIdentity()
		if err != nil {
			return "", err
		}
		if runningIdentity != nil && *runningIdentity == managedIdentity {
			mode = RestartIfVersionChanged
		} else {
			mode = RestartAlways
		}
	}
	appServerVersion, _ := probeAppServerVersionOnSocket(socketPath, ControlSocketProbeTimeout)
	managedVersion := r.managedVersion()
	if strings.TrimSpace(managedCodexBin) != "" {
		if version := ManagedCodexVersionBestEffort(managedCodexBin); version != nil {
			managedVersion = version
		}
	}
	var appServerVersionPtr *string
	if appServerVersion != "" {
		appServerVersionPtr = &appServerVersion
	}
	decision := RestartDecisionFor(mode, appServerVersionPtr, managedVersion)
	switch decision {
	case DecisionNotReady:
		return RestartNotReady, nil
	case DecisionAlreadyCurrent:
		return RestartAlreadyCurrent, nil
	case DecisionRestart:
		// Preflight the replacement before the running server is stopped, so an
		// incompatible launcher cannot leave the daemon down (Rust
		// try_restart_if_running's ensure_detached_launch).
		if err := ensureDetachedLaunchProbe(managedCodexBin); err != nil {
			return "", err
		}
		if err := discardPendingRecovery(daemon); err != nil {
			daemon.Diagnostic("warning: failed to clear stale daemon recovery before update: %v", err)
		}
		if err := stopPIDBackend(r.appServerBackend(settings), settings.ShutdownGraceSecondsValue()); err != nil {
			return "", err
		}
		if _, err := startPIDBackend(r.appServerBackend(settings)); err != nil {
			return "", err
		}
		if _, err := r.waitUntilReady(); err != nil {
			return "", err
		}
		_ = updaterRefreshMode
		// All exits for busy, not-ready or superseded selections precede cleanup.
		// Keep the operation lock, and recheck the selection under both locks.
		if root := PackageRoot(daemonCodexHome(daemon)); filepath.Base(root) == daemonPackagesDirname {
			if installLock, err := acquireExclusiveFileLock(filepath.Join(root, installLockFileName), installLockTimeout, installLockRetry, "daemon installer lock"); err == nil {
				defer installLock.Close()
				if canonicalPath(ManagedCodexBin(daemonCodexHome(daemon))) == canonicalPath(managedCodexBin) {
					pruneDaemonReleasesBestEffort(daemon, root, settings)
				}
			}
		}
		return RestartRestarted, nil
	default:
		return "", fmt.Errorf("unknown restart decision %s", decision)
	}
}

func (r *LifecycleRunner) start(settings *DaemonSettings) (*LifecycleOutput, error) {
	return r.startWithFeatureOverrides(settings, nil)
}

// startWithFeatureOverrides starts or reuses the managed app-server, replacing
// the stored feature overrides with the launch's set. Rust Daemon::start: only a
// fresh launch may replace them, and an empty set clears them.
func (r *LifecycleRunner) startWithFeatureOverrides(settings *DaemonSettings, overrides map[string]bool) (*LifecycleOutput, error) {
	daemon := r.daemon()
	socketPath := daemonSocketPath(daemon)
	if socketPath != "" {
		if version, err := probeAppServerVersionOnSocket(socketPath, ControlSocketProbeTimeout); err == nil {
			backend, err := r.runningAppServerBackend(settings)
			if err != nil {
				return nil, err
			}
			// A reused managed server still applies the saved updater preference
			// (Rust Daemon::start calls ensure_managed_updater for every backend).
			if backend != nil {
				if _, err := ensureManagedUpdater(daemon, settings); err != nil {
					daemon.Diagnostic("warning: failed to ensure managed updater after app-server start: %v", err)
				}
			}
			return daemon.LifecycleOutput(StatusAlreadyRunning, backend, nil, &version, r.managedVersion()), nil
		}
	}
	backend, err := r.runningAppServerBackend(settings)
	if err != nil {
		return nil, err
	}
	if backend != nil {
		version, err := r.waitUntilReady()
		if err != nil {
			return nil, err
		}
		if _, err := ensureManagedUpdater(daemon, settings); err != nil {
			daemon.Diagnostic("warning: failed to ensure managed updater after app-server start: %v", err)
		}
		return daemon.LifecycleOutput(StatusAlreadyRunning, backend, nil, &version, r.managedVersion()), nil
	}
	// A fresh start must ignore snapshots left by older stop clients (Rust
	// Daemon::start).
	if err := discardPendingRecovery(daemon); err != nil {
		daemon.Diagnostic("warning: failed to clear stale daemon recovery before start: %v", err)
	}
	if err := prepareManagedDaemon(daemon, settings); err != nil {
		return nil, err
	}
	daemon.refreshInstallation()
	if err := ensureManagedCodexBin(daemon.Paths.ManagedCodexBin); err != nil {
		return nil, err
	}
	if !FeatureOverridesEqual(settings.FeatureOverrides, overrides) {
		settings.FeatureOverrides = CloneFeatureOverrides(overrides)
		if err := daemon.SaveSettings(settings); err != nil {
			return nil, err
		}
	}
	pid, err := startPIDBackend(r.appServerBackend(settings))
	if err != nil {
		return nil, err
	}
	version, err := r.waitUntilReady()
	if err != nil {
		return nil, err
	}
	if _, err := ensureManagedUpdater(daemon, settings); err != nil {
		daemon.Diagnostic("warning: failed to ensure managed updater after app-server start: %v", err)
	}
	backendKind := BackendPID
	return daemon.LifecycleOutput(StatusStarted, &backendKind, pid, &version, r.managedVersion()), nil
}

func (r *LifecycleRunner) restart(settings *DaemonSettings, backend *BackendKind) (*LifecycleOutput, error) {
	daemon := r.daemon()
	socketPath := daemonSocketPath(daemon)
	if socketPath != "" {
		if _, err := probeAppServerVersionOnSocket(socketPath, ControlSocketProbeTimeout); err == nil {
			running, err := r.runningAppServerBackend(settings)
			if err != nil {
				return nil, err
			}
			if running == nil {
				return nil, errors.New("app server is running but is not managed by codex app-server daemon")
			}
		}
	}
	// A saved disabled preference retires the updater before this restart can
	// replace the selected package (Rust #43542).
	if !settings.AutoUpdateEnabled() {
		if err := stopPIDBackend(r.updateLoopBackend(settings), settings.ShutdownGraceSecondsValue()); err != nil {
			return nil, err
		}
	}
	if err := prepareManagedDaemon(daemon, settings); err != nil {
		return nil, err
	}
	daemon.refreshInstallation()
	if err := ensureManagedCodexBin(daemon.Paths.ManagedCodexBin); err != nil {
		return nil, err
	}
	running, err := r.runningAppServerBackend(settings)
	if err != nil {
		return nil, err
	}
	if running != nil {
		if err := discardPendingRecovery(daemon); err != nil {
			daemon.Diagnostic("warning: failed to clear stale daemon recovery before restart: %v", err)
		}
		if err := stopPIDBackend(r.appServerBackend(settings), settings.ShutdownGraceSecondsValue()); err != nil {
			return nil, err
		}
	}
	// Persist changed launch settings only after the old process has stopped, so
	// an interrupted restart cannot make an unapplied change look current
	// (Rust Daemon::restart_with_settings).
	if stored, err := daemon.LoadSettings(); err != nil {
		return nil, err
	} else if !daemonSettingsEqual(stored, settings) {
		if err := daemon.SaveSettings(settings); err != nil {
			return nil, err
		}
	}
	pid, err := startPIDBackend(r.appServerBackend(settings))
	if err != nil {
		return nil, err
	}
	version, err := r.waitUntilReady()
	if err != nil {
		return nil, err
	}
	if _, err := ensureManagedUpdater(daemon, settings); err != nil {
		daemon.Diagnostic("warning: failed to ensure managed updater after app-server restart: %v", err)
	}
	return daemon.LifecycleOutput(StatusRestarted, backend, pid, &version, r.managedVersion()), nil
}

func (r *LifecycleRunner) stop(settings *DaemonSettings) (*LifecycleOutput, error) {
	daemon := r.daemon()
	backend, err := r.runningAppServerBackend(settings)
	if err != nil {
		return nil, err
	}
	if backend != nil {
		if err := stopPIDBackend(r.appServerBackend(settings), settings.ShutdownGraceSecondsValue()); err != nil {
			return nil, err
		}
		return daemon.LifecycleOutput(StatusStopped, backend, nil, nil, r.managedVersion()), nil
	}
	socketPath := daemonSocketPath(daemon)
	if socketPath != "" {
		if _, err := probeAppServerVersionOnSocket(socketPath, ControlSocketProbeTimeout); err == nil {
			return nil, errors.New("app server is running but is not managed by codex app-server daemon")
		}
	}
	return daemon.LifecycleOutput(StatusNotRunning, nil, nil, nil, r.managedVersion()), nil
}

func (r *LifecycleRunner) version(settings *DaemonSettings) (*LifecycleOutput, error) {
	daemon := r.daemon()
	version, err := probeAppServerVersionOnSocket(daemonSocketPath(daemon), ControlSocketProbeTimeout)
	if err != nil {
		return nil, err
	}
	backend, err := r.runningAppServerBackend(settings)
	if err != nil {
		return nil, err
	}
	return daemon.LifecycleOutput(StatusRunning, backend, nil, &version, r.managedVersion()), nil
}

func (r *LifecycleRunner) waitUntilReady() (string, error) {
	daemon := r.daemon()
	socketPath := daemonSocketPath(daemon)
	deadline := time.Now().Add(lifecycleReadyTimeout)
	var lastErr error
	for {
		version, err := probeAppServerVersionOnSocket(socketPath, ControlSocketProbeTimeout)
		if err == nil {
			return version, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			tail, _ := readStderrLogTail(daemon.Paths.PIDFile)
			return "", fmt.Errorf("%w: %s", lastErr, daemon.AppServerNotReadyContext(r.managedVersion(), tail))
		}
		time.Sleep(lifecycleReadyRetry)
	}
}

func (r *LifecycleRunner) acquireOperationLock() (*daemonFileLock, error) {
	daemon := r.daemon()
	if err := codexuds.PreparePrivateSocketDirectory(filepath.Dir(daemon.Paths.OperationLockFile)); err != nil {
		return nil, err
	}
	return acquireExclusiveFileLock(daemon.Paths.OperationLockFile, OperationLockTimeout, OperationLockRetry, "daemon operation lock")
}

func (r *LifecycleRunner) tryAcquireOperationLock() (*daemonFileLock, bool, error) {
	daemon := r.daemon()
	if err := codexuds.PreparePrivateSocketDirectory(filepath.Dir(daemon.Paths.OperationLockFile)); err != nil {
		return nil, false, err
	}
	return tryAcquireExclusiveFileLock(daemon.Paths.OperationLockFile, "daemon operation lock")
}

func (r *LifecycleRunner) appServerBackend(settings *DaemonSettings) *PIDBackend {
	return NewPIDBackend(r.daemon().BackendPaths(settings))
}

func (r *LifecycleRunner) updateLoopBackend(settings *DaemonSettings) *PIDBackend {
	return NewPIDUpdateLoopBackend(r.daemon().BackendPaths(settings))
}

func backendFromVersion(version *string) *BackendKind {
	if version == nil {
		return nil
	}
	backend := BackendPID
	return &backend
}

func EnsureManagedCodexBin(path string) error {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		// Rust ensure_managed_codex_bin: on Windows the managed launch is probed
		// before a daemon is started or stopped, so a launch the caller's Job
		// Object forbids never takes the current daemon down.
		if err := ensureDetachedLaunch(path); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("daemon executable not found at %s; repair the existing installation, or run `codex app-server daemon start` to install a missing daemon", path)
}

func ManagedCodexVersionBestEffort(path string) *string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	versionCmd := exec.Command(path, "--version")
	// Rust #48483: the version probe is piped, so it must not allocate a console
	// window when the daemon itself has no console.
	envutil.SuppressConsoleWindow(versionCmd)
	output, err := versionCmd.Output()
	if err != nil {
		return nil
	}
	version, err := ParseManagedCodexVersion(string(output))
	if err != nil {
		return nil
	}
	return &version
}

func (r *LifecycleRunner) removePIDFile() error {
	daemon := r.daemon()
	if err := os.Remove(daemon.Paths.PIDFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (r *LifecycleRunner) daemon() *Daemon {
	if r == nil || r.Daemon == nil {
		return NewDaemon(&Paths{}, "")
	}
	return r.Daemon
}

func (r *LifecycleRunner) now() time.Time {
	if r == nil || r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func (r *LifecycleRunner) managedVersion() *string {
	return managedCodexVersionBestEffort(r.daemon().Paths.ManagedCodexBin)
}

func cliVersionString(daemon *Daemon) string {
	if daemon == nil {
		return ""
	}
	return daemon.CLIVersion
}
