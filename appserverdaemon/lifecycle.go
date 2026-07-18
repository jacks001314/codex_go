package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type LifecycleRunner struct {
	Daemon *Daemon
	Now    func() time.Time
}

const (
	OperationLockTimeout = 75 * time.Second
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
	pidBackendIsStartingOrRunning = func(backend *PIDBackend) (bool, error) { return backend.IsStartingOrRunning() }
	startPIDBackend               = func(backend *PIDBackend) (*uint32, error) { return backend.Start() }
	stopPIDBackend                = func(backend *PIDBackend) error { return backend.Stop() }
	lifecycleReadyTimeout         = RemoteControlReadyTimeout
	lifecycleReadyRetry           = 50 * time.Millisecond
	readStderrLogTail             = ReadStderrLogTail
)

func (r *LifecycleRunner) Run(command LifecycleCommand) (*LifecycleOutput, error) {
	if r == nil {
		r = &LifecycleRunner{}
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
		settings, err := daemon.LoadSettings()
		if err != nil {
			return nil, err
		}
		return r.stop(settings)
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
	lock, err := r.acquireOperationLock()
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	return r.bootstrapLocked(options)
}

func (r *LifecycleRunner) bootstrapLocked(options *BootstrapOptions) (*BootstrapOutput, error) {
	daemon := r.daemon()
	if err := ensureManagedCodexBin(daemon.Paths.ManagedCodexBin); err != nil {
		return nil, err
	}
	settings := &DaemonSettings{}
	if options != nil {
		settings.RemoteControlEnabled = options.RemoteControlEnabled
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
	if err := daemon.SaveSettings(settings); err != nil {
		return nil, err
	}
	if running, err := pidBackendIsStartingOrRunning(r.appServerBackend(settings)); err != nil {
		return nil, err
	} else if running {
		if err := stopPIDBackend(r.appServerBackend(settings)); err != nil {
			return nil, err
		}
	}
	if _, err := startPIDBackend(r.appServerBackend(settings)); err != nil {
		return nil, err
	}
	if running, err := pidBackendIsStartingOrRunning(r.updateLoopBackend(settings)); err != nil {
		return nil, err
	} else if running {
		if err := stopPIDBackend(r.updateLoopBackend(settings)); err != nil {
			return nil, err
		}
	}
	if _, err := startPIDBackend(r.updateLoopBackend(settings)); err != nil {
		return nil, err
	}
	appServerVersion, err := r.waitUntilReady()
	if err != nil {
		return nil, err
	}
	managedVersion := r.managedVersion()
	return daemon.BootstrapOutput(settings, appServerVersion, managedVersion), nil
}

func (r *LifecycleRunner) EnsureRemoteControlStarted() (*RemoteControlStartOutput, error) {
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
		if err := stopPIDBackend(r.appServerBackend(&DaemonSettings{RemoteControlEnabled: previous})); err != nil {
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
		if err := stopPIDBackend(r.appServerBackend(settings)); err != nil {
			return "", err
		}
		if _, err := startPIDBackend(r.appServerBackend(settings)); err != nil {
			return "", err
		}
		if _, err := r.waitUntilReady(); err != nil {
			return "", err
		}
		_ = updaterRefreshMode
		return RestartRestarted, nil
	default:
		return "", fmt.Errorf("unknown restart decision %s", decision)
	}
}

func (r *LifecycleRunner) start(settings *DaemonSettings) (*LifecycleOutput, error) {
	daemon := r.daemon()
	socketPath := daemonSocketPath(daemon)
	if socketPath != "" {
		if version, err := probeAppServerVersionOnSocket(socketPath, ControlSocketProbeTimeout); err == nil {
			backend, err := r.runningAppServerBackend(settings)
			if err != nil {
				return nil, err
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
		return daemon.LifecycleOutput(StatusAlreadyRunning, backend, nil, &version, r.managedVersion()), nil
	}
	if err := ensureManagedCodexBin(daemon.Paths.ManagedCodexBin); err != nil {
		return nil, err
	}
	pid, err := startPIDBackend(r.appServerBackend(settings))
	if err != nil {
		return nil, err
	}
	version, err := r.waitUntilReady()
	if err != nil {
		return nil, err
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
	if err := ensureManagedCodexBin(daemon.Paths.ManagedCodexBin); err != nil {
		return nil, err
	}
	running, err := r.runningAppServerBackend(settings)
	if err != nil {
		return nil, err
	}
	if running != nil {
		if err := stopPIDBackend(r.appServerBackend(settings)); err != nil {
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
	return daemon.LifecycleOutput(StatusRestarted, backend, pid, &version, r.managedVersion()), nil
}

func (r *LifecycleRunner) stop(settings *DaemonSettings) (*LifecycleOutput, error) {
	daemon := r.daemon()
	backend, err := r.runningAppServerBackend(settings)
	if err != nil {
		return nil, err
	}
	if backend != nil {
		if err := stopPIDBackend(r.appServerBackend(settings)); err != nil {
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
	return acquireExclusiveFileLock(daemon.Paths.OperationLockFile, OperationLockTimeout, OperationLockRetry, "daemon operation lock")
}

func (r *LifecycleRunner) tryAcquireOperationLock() (*daemonFileLock, bool, error) {
	daemon := r.daemon()
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
		return nil
	}
	return fmt.Errorf("managed standalone Codex install not found at %s\n\nThis command requires the standalone install managed by the Codex installer, because the daemon starts and updates app-server from that fixed path.\n\nInstall it with:\n  curl -fsSL https://chatgpt.com/codex/install.sh | sh\n\nThen rerun the command you just tried.", path)
}

func ManagedCodexVersionBestEffort(path string) *string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	output, err := exec.Command(path, "--version").Output()
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
	return ManagedCodexVersionBestEffort(r.daemon().Paths.ManagedCodexBin)
}

func cliVersionString(daemon *Daemon) string {
	if daemon == nil {
		return ""
	}
	return daemon.CLIVersion
}
