package appserverdaemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codex_go/codexuds"
	"codex_go/envutil"
	"codex_go/install"
)

const (
	InitialUpdateDelay    = 5 * time.Minute
	RestartRetryInterval  = 50 * time.Millisecond
	UpdateInterval        = time.Hour
	InstallScriptEndpoint = "https://chatgpt.com/codex/install.sh"
)

type UpdateLoopControl string

const (
	UpdateLoopContinue UpdateLoopControl = "continue"
	UpdateLoopStop     UpdateLoopControl = "stop"
)

type UpdateLoopOptions struct {
	InitialDelay time.Duration
	UpdateDelay  time.Duration
	RetryDelay   time.Duration
	// Install runs one guarded installer update for a package root
	// (Rust update_once's installer half).
	Install func(ctx context.Context, mode installerMode, packageRoot string) error
	// FetchScript and RunInstaller are the installer edges Install composes,
	// injectable so tests can assert the guards without running an installer.
	FetchScript   func(context.Context) ([]byte, error)
	RunInstaller  func(context.Context, []byte, installerMode, string, string) error
	CurrentExe    func() (string, error)
	ReadFile      func(string) ([]byte, error)
	ReexecUpdater func(string) error
	// RestoreRelease authorizes one production restore, which the CLI passes
	// when it starts a one-shot worker for a pinned package.
	RestoreRelease string
}

func DefaultUpdateLoopOptions() *UpdateLoopOptions {
	options := &UpdateLoopOptions{
		InitialDelay:  InitialUpdateDelay,
		UpdateDelay:   UpdateInterval,
		RetryDelay:    RestartRetryInterval,
		FetchScript:   fetchInstallerScript,
		RunInstaller:  runInstallerScript,
		CurrentExe:    os.Executable,
		ReadFile:      os.ReadFile,
		ReexecUpdater: ReexecManagedUpdater,
	}
	options.Install = func(ctx context.Context, mode installerMode, packageRoot string) error {
		return installWithGuards(ctx, mode, packageRoot, packageRootCodexHome(packageRoot), options.FetchScript, options.RunInstaller)
	}
	return options
}

func RunPIDUpdateLoop(ctx context.Context, runner *LifecycleRunner, options *UpdateLoopOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := EnsureNonElevated(); err != nil {
		return err
	}
	if runner == nil {
		runner = &LifecycleRunner{}
	}
	options = normalizeUpdateLoopOptions(options)
	runningIdentity, err := CurrentUpdaterIdentity(options)
	if err != nil {
		return err
	}
	// The detached updater has no control socket, so its owner asks it to stop
	// through CODEX_DAEMON_SHUTDOWN_FILE (Rust update_loop::Signal). The watcher
	// is a no-op unless that env var is set.
	ctx, cancelLoop := context.WithCancel(ctx)
	defer cancelLoop()
	go WatchDaemonShutdownRequest(ctx, cancelLoop)
	daemon := runner.daemon()
	// On Windows the launcher waits for this updater's readiness acknowledgment,
	// so ownership is claimed before serving and acknowledged after the request
	// socket is bound (Rust update_loop::run_with_http).
	ownershipBackend := runner.updateLoopBackend(&DaemonSettings{})
	if err := WaitForUpdaterOwnership(ownershipBackend); err != nil {
		return err
	}
	restoreRelease := strings.TrimSpace(options.RestoreRelease)
	trigger := UpdateTrigger{Kind: UpdateTriggerManual}
	if restoreRelease != "" {
		// Only a CLI launch under the operation lock hands this authority over.
		trigger = UpdateTrigger{Kind: UpdateTriggerRestoreProduction, Release: restoreRelease}
	}
	listener, socketPath, err := listenManualUpdateSocket(daemon)
	if err != nil {
		return err
	}
	if err := MarkUpdaterReady(ownershipBackend); err != nil {
		return err
	}
	if listener != nil {
		defer listener.Close()
	}
	requests := make(chan net.Conn, 1)
	acceptFailures := make(chan error, 1)
	if listener != nil {
		go func() {
			for {
				conn, err := listener.Accept()
				if err != nil {
					acceptFailures <- err
					return
				}
				requests <- conn
			}
		}()
	}
	nextCheck := time.Now().Add(options.InitialDelay)
	if restoreRelease != "" || !autoUpdateEnabled(daemon) {
		// A one-shot worker or a disabled updater only stays long enough for the
		// launch that started it to send its request.
		nextCheck = time.Now().Add(updaterOneShotDelay)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-acceptFailures:
			return fmt.Errorf("failed to accept updater request on %s: %w", socketPath, err)
		case conn := <-requests:
			disposition, err := handleManualUpdateConnection(conn, runner, options, runningIdentity, trigger)
			if trigger.Kind == UpdateTriggerRestoreProduction {
				// The restore authority is single use.
				restoreRelease = ""
				trigger = UpdateTrigger{Kind: UpdateTriggerManual}
			}
			if err != nil {
				daemon.Diagnostic("warning: failed to serve the daemon updater request: %v", err)
			}
			switch disposition {
			case manualUpdateStop:
				return nil
			case manualUpdateUnchanged:
				continue
			}
			// A manual update ran: re-check soon instead of waiting a full interval
			// so the schedule reflects the new selection.
			nextCheck = time.Now()
		case <-time.After(time.Until(nextCheck)):
			if restoreRelease != "" {
				// The authorizing CLI may have exited before sending its request.
				return nil
			}
			enabled, err := readAutoUpdateEnabled(daemon)
			if err != nil {
				nextCheck = time.Now().Add(updaterSettingsRetryDelay)
				continue
			}
			if !enabled {
				return nil
			}
			control, err := UpdateOnce(ctx, runner, runningIdentity, options)
			if err != nil && errors.Is(err, context.Canceled) {
				return nil
			}
			if control == UpdateLoopStop {
				return nil
			}
			delay, ok := nextUpdateDelay(daemon)
			if !ok {
				return nil
			}
			nextCheck = time.Now().Add(delay)
		}
	}
}

const (
	// updaterOneShotDelay is how long a one-shot or disabled updater waits for
	// the launch that started it to request an update.
	updaterOneShotDelay = 15 * time.Second
	// updaterSettingsRetryDelay is how long the loop waits after unreadable
	// settings before checking again.
	updaterSettingsRetryDelay = time.Minute
)

// readAutoUpdateEnabled mirrors Rust's tolerant UpdaterSettings::load: settings
// that cannot be read are retried, not fatal.
func readAutoUpdateEnabled(daemon *Daemon) (bool, error) {
	settings, err := daemon.LoadSettings()
	if err != nil {
		return false, err
	}
	return settings.AutoUpdateEnabled(), nil
}

func autoUpdateEnabled(daemon *Daemon) bool {
	enabled, err := readAutoUpdateEnabled(daemon)
	if err != nil {
		return true
	}
	return enabled
}

// listenManualUpdateSocket binds the private request socket manual updates are
// served on (Rust update_loop::run_with_http).
func listenManualUpdateSocket(daemon *Daemon) (net.Listener, string, error) {
	socketPath := manualUpdateSocketPath(daemon)
	if socketPath == "" {
		return nil, "", nil
	}
	if err := codexuds.PreparePrivateSocketDirectory(filepath.Dir(socketPath)); err != nil {
		return nil, socketPath, err
	}
	if _, err := os.Lstat(socketPath); err == nil {
		if err := os.Remove(socketPath); err != nil {
			return nil, socketPath, fmt.Errorf("failed to clear the stale updater socket %s: %w", socketPath, err)
		}
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, socketPath, fmt.Errorf("failed to bind the updater request socket %s: %w", socketPath, err)
	}
	return listener, socketPath, nil
}

func UpdateOnce(ctx context.Context, runner *LifecycleRunner, runningIdentity *install.ExecutableIdentity, options *UpdateLoopOptions) (UpdateLoopControl, error) {
	control, _, reexecBin, err := updateOnce(ctx, runner, runningIdentity, options, UpdateTrigger{Kind: UpdateTriggerScheduled})
	if err != nil {
		return control, err
	}
	if strings.TrimSpace(reexecBin) != "" {
		// The managed binary changed, so this updater replaces itself with it and
		// stops serving (Rust adopt_managed_updater / reexec_managed_updater).
		return UpdateLoopStop, options.ReexecUpdater(reexecBin)
	}
	return control, err
}

// updateOnce runs one update for the given trigger and reports which restart
// outcome the daemon reached, which the manual lane turns into its message
// (Rust update_loop::update_once). The reexec request is returned instead of
// acted on so a manual responder can answer before the process is replaced.
func updateOnce(ctx context.Context, runner *LifecycleRunner, runningIdentity *install.ExecutableIdentity, options *UpdateLoopOptions, trigger UpdateTrigger) (UpdateLoopControl, *RestartIfRunningOutcome, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runner == nil {
		runner = &LifecycleRunner{}
	}
	options = normalizeUpdateLoopOptions(options)
	daemon := runner.daemon()
	settings, err := daemon.LoadSettings()
	if err != nil {
		return UpdateLoopContinue, nil, "", err
	}
	if trigger.Kind == UpdateTriggerScheduled && !settings.AutoUpdateEnabled() {
		return UpdateLoopStop, nil, "", nil
	}
	unstable, err := releaseSelectionUnstable(daemon, trigger)
	if err != nil {
		return UpdateLoopContinue, nil, "", err
	}
	if unstable {
		// An installer can be between changing `current` and publishing its
		// latest-channel marker; retry after the interval instead of exiting.
		return UpdateLoopContinue, nil, "", nil
	}
	root, previousRelease, previousName, err := selectedRelease(daemon)
	if err != nil {
		return UpdateLoopContinue, nil, "", err
	}
	// The installer's "previous release" and the restore trigger carry the
	// release *name*, not its path (Rust InstallerMode::Update(&previous_release)).
	mode := installerMode{Kind: installerUpdate, Release: previousName}
	if trigger.Kind == UpdateTriggerRestoreProduction {
		if previousName != trigger.Release {
			return UpdateLoopContinue, nil, "", errors.New("daemon selection changed; retry the update")
		}
		mode = installerMode{Kind: installerRestoreProduction, Release: previousName}
	}
	if err := options.Install(ctx, mode, root); err != nil {
		return UpdateLoopContinue, nil, "", err
	}
	if trigger.Kind != UpdateTriggerScheduled &&
		!isStableStandaloneRelease(daemonCodexHome(daemon), ManagedCodexBin(daemonCodexHome(daemon))) {
		return UpdateLoopContinue, nil, "", errors.New("installer did not select a stable latest release; retry the update")
	}
	unstable, err = releaseSelectionUnstable(daemon, trigger)
	if err != nil {
		return UpdateLoopContinue, nil, "", err
	}
	if unstable {
		return UpdateLoopContinue, nil, "", nil
	}
	managedBin := ManagedCodexBin(daemonCodexHome(daemon))
	managedIdentity, err := ManagedExecutableIdentity(managedBin, options)
	if err != nil {
		return UpdateLoopContinue, nil, "", err
	}
	restartMode, updaterRefreshMode := UpdateModesForIdentities(runningIdentity, managedIdentity)
	releaseChanged := false
	if _, currentRelease, _, err := selectedRelease(daemon); err == nil {
		releaseChanged = currentRelease != previousRelease
	}
	if runningIdentity != nil && managedIdentity != nil {
		restartMode = restartModeForTrigger(trigger, releaseChanged, *managedIdentity, *runningIdentity)
	}
	for {
		if ctx.Err() != nil {
			return UpdateLoopStop, nil, "", ctx.Err()
		}
		outcome, err := runner.TryRestartIfRunning(restartMode, updaterRefreshMode, managedBin)
		if err != nil {
			return UpdateLoopContinue, nil, "", err
		}
		if ShouldReexecUpdater(updaterRefreshMode, outcome) {
			// The caller acts on this: the scheduled loop replaces itself, while a
			// manual responder answers its request first.
			return UpdateLoopStop, &outcome, managedBin, nil
		}
		switch outcome {
		case RestartRestarted, RestartNotRunning:
			return UpdateLoopContinue, &outcome, "", nil
		case RestartAlreadyCurrent:
			if trigger.Kind == UpdateTriggerScheduled {
				return updateLoopSelectionOutcome(daemon), nil, "", nil
			}
			if restartMode != RestartIfBinaryOrVersionChanged {
				return UpdateLoopContinue, nil, "", errors.New("managed daemon could not restart; retry when it is ready")
			}
			managedNow := ManagedCodexBin(daemonCodexHome(daemon))
			if !isStableStandaloneRelease(daemonCodexHome(daemon), managedNow) ||
				canonicalPath(managedNow) != canonicalPath(managedBin) {
				return UpdateLoopContinue, nil, "", errors.New("managed daemon changed during the update; retry")
			}
			return UpdateLoopContinue, &outcome, "", nil
		case RestartNotReady:
			if trigger.Kind != UpdateTriggerScheduled {
				return UpdateLoopContinue, nil, "", errors.New("managed daemon could not restart; retry when it is ready")
			}
			return updateLoopSelectionOutcome(daemon), nil, "", nil
		default:
			if sleepOrDone(ctx, options.RetryDelay) {
				return UpdateLoopStop, nil, "", nil
			}
		}
	}
}

// updateLoopSelectionOutcome mirrors Rust's scheduled fallback: keep following
// an installer-published release, but stop once the selection is no longer one.
func updateLoopSelectionOutcome(daemon *Daemon) UpdateLoopControl {
	if isStableStandaloneRelease(daemonCodexHome(daemon), ManagedCodexBin(daemonCodexHome(daemon))) {
		return UpdateLoopContinue
	}
	return UpdateLoopStop
}

func CurrentUpdaterIdentity(options *UpdateLoopOptions) (*install.ExecutableIdentity, error) {
	options = normalizeUpdateLoopOptions(options)
	currentExe, err := options.CurrentExe()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve current updater executable: %w", err)
	}
	return ManagedExecutableIdentity(currentExe, options)
}

func ManagedExecutableIdentity(path string, options *UpdateLoopOptions) (*install.ExecutableIdentity, error) {
	options = normalizeUpdateLoopOptions(options)
	data, err := options.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read executable identity for %s: %w", path, err)
	}
	identity := install.ExecutableIdentityFromBytes(data)
	return &identity, nil
}

func ReexecManagedUpdater(managedCodexBin string) error {
	if managedCodexBin == "" {
		return fmt.Errorf("managed Codex binary path is empty")
	}
	pidFile := os.Getenv(UpdaterPIDFileEnv)
	if pidFile == "" {
		command := exec.Command(managedCodexBin, "app-server", "daemon", "pid-update-loop")
		// Rust #48483: the updater probe is piped, so it must not allocate a
		// console window when the CLI itself has no console.
		envutil.SuppressConsoleWindow(command)
		return command.Run()
	}
	// Rust #42392: start the successor updater detached and wait until it
	// claims the update PID record (readiness handshake). If it never becomes
	// ready, terminate it and return the error so the current updater keeps
	// running with the old PID record intact.
	backend := NewPIDUpdateLoopBackend(BackendPaths{
		CodexBin: managedCodexBin,
		PIDFile:  pidFile,
	})
	previousRecord, _ := ReadPIDRecord(pidFile)
	pid, _, err := startDetachedPIDProcess(backend)
	if err != nil {
		return fmt.Errorf("failed to start successor updater: %w", err)
	}
	deadline := time.Now().Add(PIDStartTimeout)
	for {
		state, err := ReadPIDFileState(pidFile)
		if err == nil && state.Kind == PIDFileRunning && state.Record != nil && state.Record.PID == pid {
			if active, err := processMatchesPIDRecord(state.Record); err == nil && active {
				return nil
			}
		}
		if time.Now().After(deadline) {
			_ = forceTerminatePIDProcess(pid, false)
			if previousRecord != nil {
				if previous, activeErr := processMatchesPIDRecord(previousRecord); activeErr == nil && previous {
					_ = WritePIDRecord(pidFile, previousRecord)
				}
			}
			return fmt.Errorf("successor updater %d did not become ready on %s", pid, pidFile)
		}
		time.Sleep(PIDStartPollInterval)
	}
}

func ShouldReexecUpdater(mode UpdaterRefreshMode, outcome RestartIfRunningOutcome) bool {
	return mode == UpdaterRefreshReexecIfManagedBinaryChanged && outcome == RestartRestarted
}

func normalizeUpdateLoopOptions(options *UpdateLoopOptions) *UpdateLoopOptions {
	if options == nil {
		return DefaultUpdateLoopOptions()
	}
	defaults := DefaultUpdateLoopOptions()
	if options.InitialDelay == 0 {
		options.InitialDelay = defaults.InitialDelay
	}
	if options.UpdateDelay == 0 {
		options.UpdateDelay = defaults.UpdateDelay
	}
	if options.RetryDelay == 0 {
		options.RetryDelay = defaults.RetryDelay
	}
	if options.FetchScript == nil {
		options.FetchScript = defaults.FetchScript
	}
	if options.RunInstaller == nil {
		options.RunInstaller = defaults.RunInstaller
	}
	if options.Install == nil {
		fetch, run := options.FetchScript, options.RunInstaller
		options.Install = func(ctx context.Context, mode installerMode, packageRoot string) error {
			return installWithGuards(ctx, mode, packageRoot, packageRootCodexHome(packageRoot), fetch, run)
		}
	}
	if options.CurrentExe == nil {
		options.CurrentExe = defaults.CurrentExe
	}
	if options.ReadFile == nil {
		options.ReadFile = defaults.ReadFile
	}
	if options.ReexecUpdater == nil {
		options.ReexecUpdater = defaults.ReexecUpdater
	}
	return options
}

func sleepOrDone(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return false
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
}
