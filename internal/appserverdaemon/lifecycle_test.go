package appserverdaemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLifecycleRunnerStartVersionStop(t *testing.T) {
	stubLifecycleManagedDaemon(t)
	daemon := NewDaemonForCodexHome(t.TempDir(), "codex-go-test")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }

	started, err := runner.Run(LifecycleStart)
	if err != nil {
		t.Fatalf("Run(start) error = %v", err)
	}
	if started.Status != StatusStarted || started.PID == nil {
		t.Fatalf("start output = %#v", started)
	}

	version, err := runner.Run(LifecycleVersion)
	if err != nil {
		t.Fatalf("Run(version) error = %v", err)
	}
	if version.Status != StatusRunning || version.PID != nil || version.AppServerVersion == nil || *version.AppServerVersion != "9.9.9" {
		t.Fatalf("version output = %#v, start = %#v", version, started)
	}

	stopped, err := runner.Run(LifecycleStop)
	if err != nil {
		t.Fatalf("Run(stop) error = %v", err)
	}
	if stopped.Status != StatusStopped {
		t.Fatalf("stop output = %#v", stopped)
	}
	if _, err := os.Stat(daemon.Paths.PIDFile); !os.IsNotExist(err) {
		t.Fatalf("pid stat error = %v, want not exist", err)
	}
}

func TestLifecycleRunnerBootstrapAndRemoteControlSettings(t *testing.T) {
	stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	runner := NewLifecycleRunner(daemon)

	bootstrapped, err := runner.Bootstrap(&BootstrapOptions{RemoteControlEnabled: true})
	if err != nil {
		t.Fatalf("Bootstrap error = %v", err)
	}
	if bootstrapped.Status != BootstrapBootstrapped || !bootstrapped.RemoteControlEnabled {
		t.Fatalf("bootstrap output = %#v", bootstrapped)
	}

	settings, err := LoadSettings(filepath.Join(home, StateDirName, SettingsFileName))
	if err != nil {
		t.Fatalf("LoadSettings error = %v", err)
	}
	if !settings.RemoteControlEnabled {
		t.Fatal("remote control setting not persisted")
	}

	disabled, err := runner.SetRemoteControl(RemoteControlDisabled)
	if err != nil {
		t.Fatalf("SetRemoteControl disabled error = %v", err)
	}
	if disabled.Status != RemoteStatusDisabled || disabled.RemoteControlEnabled {
		t.Fatalf("disabled output = %#v", disabled)
	}
	if disabled.Backend == nil || *disabled.Backend != BackendPID {
		t.Fatalf("disabled backend = %#v, want pid", disabled.Backend)
	}
	if disabled.AppServerVersion == nil || *disabled.AppServerVersion != "9.9.9" {
		t.Fatalf("disabled app server version = %#v", disabled.AppServerVersion)
	}
}

func TestLifecycleRunnerSetRemoteControlRestartsRunningDaemon(t *testing.T) {
	stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }

	started, err := runner.Run(LifecycleStart)
	if err != nil {
		t.Fatalf("Run(start) error = %v", err)
	}
	if started.PID == nil {
		t.Fatalf("started output = %#v", started)
	}

	enabled, err := runner.SetRemoteControl(RemoteControlEnabled)
	if err != nil {
		t.Fatalf("SetRemoteControl enabled error = %v", err)
	}
	if enabled.Status != RemoteStatusEnabled || !enabled.RemoteControlEnabled {
		t.Fatalf("enabled output = %#v", enabled)
	}
	if enabled.Backend == nil || *enabled.Backend != BackendPID {
		t.Fatalf("enabled backend = %#v, want pid", enabled.Backend)
	}
	if enabled.AppServerVersion == nil || *enabled.AppServerVersion != "9.9.9" {
		t.Fatalf("enabled app server version = %#v", enabled.AppServerVersion)
	}

	state, err := ReadPIDFileState(daemon.Paths.PIDFile)
	if err != nil {
		t.Fatalf("ReadPIDFileState error = %v", err)
	}
	if state.Kind != PIDFileMissing {
		t.Fatalf("pid state = %#v, want missing under stub backend", state)
	}
}

func TestLifecycleRunnerStartExistingSocketReturnsAlreadyRunningWithoutBackend(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	stub.socketReady = true
	daemon := NewDaemonForCodexHome(t.TempDir(), "codex-go-test")
	runner := NewLifecycleRunner(daemon)

	output, err := runner.Run(LifecycleStart)
	if err != nil {
		t.Fatalf("Run(start) error = %v", err)
	}
	if output.Status != StatusAlreadyRunning || output.Backend != nil || output.PID != nil {
		t.Fatalf("output = %#v", output)
	}
	if output.AppServerVersion == nil || *output.AppServerVersion != "9.9.9" {
		t.Fatalf("app server version = %#v", output.AppServerVersion)
	}
}

func TestLifecycleRunnerBootstrapRejectsUnmanagedSocket(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	stub.socketReady = true
	daemon := NewDaemonForCodexHome(t.TempDir(), "codex-go-test")
	runner := NewLifecycleRunner(daemon)

	_, err := runner.Bootstrap(&BootstrapOptions{RemoteControlEnabled: true})
	if err == nil || err.Error() != "app server is running but is not managed by codex app-server daemon" {
		t.Fatalf("Bootstrap error = %v", err)
	}
}

func TestLifecycleRunnerRestartRejectsUnmanagedSocket(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	stub.socketReady = true
	daemon := NewDaemonForCodexHome(t.TempDir(), "codex-go-test")
	runner := NewLifecycleRunner(daemon)

	_, err := runner.Run(LifecycleRestart)
	if err == nil || err.Error() != "app server is running but is not managed by codex app-server daemon" {
		t.Fatalf("Run(restart) error = %v", err)
	}
}

func TestLifecycleRunnerStopRejectsUnmanagedSocket(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	stub.socketReady = true
	daemon := NewDaemonForCodexHome(t.TempDir(), "codex-go-test")
	runner := NewLifecycleRunner(daemon)

	_, err := runner.Run(LifecycleStop)
	if err == nil || err.Error() != "app server is running but is not managed by codex app-server daemon" {
		t.Fatalf("Run(stop) error = %v", err)
	}
}

func TestLifecycleRunnerSetRemoteControlAlreadyEnabledRunningDaemonSendsSocketRPC(t *testing.T) {
	stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }

	if _, err := runner.Bootstrap(&BootstrapOptions{RemoteControlEnabled: true}); err != nil {
		t.Fatalf("Bootstrap error = %v", err)
	}
	if _, err := runner.Run(LifecycleStart); err != nil {
		t.Fatalf("Run(start) error = %v", err)
	}

	var probedPath string
	var enabledPath string
	stubRemoteControlSocketCalls(t,
		func(path string, timeout time.Duration) (string, error) {
			probedPath = path
			if timeout != RemoteControlReadyTimeout {
				t.Fatalf("probe timeout = %v, want %v", timeout, RemoteControlReadyTimeout)
			}
			return "1.2.3", nil
		},
		func(path string, timeout time.Duration, retry time.Duration) (RemoteControlReadyStatus, error) {
			enabledPath = path
			if timeout != RemoteControlReadyTimeout || retry != 50*time.Millisecond {
				t.Fatalf("enable timeout/retry = %v/%v", timeout, retry)
			}
			return RemoteControlReadyStatus{}, nil
		},
		func(path string, timeout time.Duration, retry time.Duration) (RemoteControlReadyStatus, error) {
			t.Fatalf("disable called with %s", path)
			return RemoteControlReadyStatus{}, nil
		},
	)

	output, err := runner.SetRemoteControl(RemoteControlEnabled)
	if err != nil {
		t.Fatalf("SetRemoteControl enabled error = %v", err)
	}
	if output.Status != RemoteStatusAlreadyEnabled || !output.RemoteControlEnabled {
		t.Fatalf("output = %#v", output)
	}
	if output.Backend == nil || *output.Backend != BackendPID {
		t.Fatalf("backend = %#v, want pid", output.Backend)
	}
	if output.AppServerVersion == nil || *output.AppServerVersion != "1.2.3" {
		t.Fatalf("app server version = %#v", output.AppServerVersion)
	}
	if probedPath != daemon.Paths.SocketPath || enabledPath != daemon.Paths.SocketPath {
		t.Fatalf("socket calls probed=%q enabled=%q want %q", probedPath, enabledPath, daemon.Paths.SocketPath)
	}
}

func TestLifecycleRunnerSetRemoteControlAlreadyDisabledRunningDaemonSendsSocketRPC(t *testing.T) {
	stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }

	if _, err := runner.Run(LifecycleStart); err != nil {
		t.Fatalf("Run(start) error = %v", err)
	}

	var probedPath string
	var disabledPath string
	stubRemoteControlSocketCalls(t,
		func(path string, timeout time.Duration) (string, error) {
			probedPath = path
			return "2.0.0", nil
		},
		func(path string, timeout time.Duration, retry time.Duration) (RemoteControlReadyStatus, error) {
			t.Fatalf("enable called with %s", path)
			return RemoteControlReadyStatus{}, nil
		},
		func(path string, timeout time.Duration, retry time.Duration) (RemoteControlReadyStatus, error) {
			disabledPath = path
			if timeout != RemoteControlReadyTimeout || retry != 50*time.Millisecond {
				t.Fatalf("disable timeout/retry = %v/%v", timeout, retry)
			}
			return RemoteControlReadyStatus{}, nil
		},
	)

	output, err := runner.SetRemoteControl(RemoteControlDisabled)
	if err != nil {
		t.Fatalf("SetRemoteControl disabled error = %v", err)
	}
	if output.Status != RemoteStatusAlreadyDisabled || output.RemoteControlEnabled {
		t.Fatalf("output = %#v", output)
	}
	if output.Backend == nil || *output.Backend != BackendPID {
		t.Fatalf("backend = %#v, want pid", output.Backend)
	}
	if output.AppServerVersion == nil || *output.AppServerVersion != "2.0.0" {
		t.Fatalf("app server version = %#v", output.AppServerVersion)
	}
	if probedPath != daemon.Paths.SocketPath || disabledPath != daemon.Paths.SocketPath {
		t.Fatalf("socket calls probed=%q disabled=%q want %q", probedPath, disabledPath, daemon.Paths.SocketPath)
	}
}

func TestLifecycleRunnerSetRemoteControlRejectsUnmanagedSocket(t *testing.T) {
	daemon := NewDaemonForCodexHome(t.TempDir(), "codex-go-test")
	runner := NewLifecycleRunner(daemon)

	stubRemoteControlSocketCalls(t,
		func(path string, timeout time.Duration) (string, error) {
			if path != daemon.Paths.SocketPath {
				t.Fatalf("probe path = %q, want %q", path, daemon.Paths.SocketPath)
			}
			if timeout != ControlSocketProbeTimeout {
				t.Fatalf("probe timeout = %v, want %v", timeout, ControlSocketProbeTimeout)
			}
			return "3.0.0", nil
		},
		func(path string, timeout time.Duration, retry time.Duration) (RemoteControlReadyStatus, error) {
			t.Fatalf("enable called with %s", path)
			return RemoteControlReadyStatus{}, nil
		},
		func(path string, timeout time.Duration, retry time.Duration) (RemoteControlReadyStatus, error) {
			t.Fatalf("disable called with %s", path)
			return RemoteControlReadyStatus{}, nil
		},
	)

	_, err := runner.SetRemoteControl(RemoteControlEnabled)
	if err == nil || err.Error() != "app server is running but is not managed by codex app-server daemon" {
		t.Fatalf("error = %v", err)
	}
}

func TestLifecycleRunnerEnsureRemoteControlStarted(t *testing.T) {
	stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }

	first, err := runner.EnsureRemoteControlStarted()
	if err != nil {
		t.Fatalf("EnsureRemoteControlStarted first error = %v", err)
	}
	if first.Bootstrap == nil || first.Bootstrap.Status != BootstrapBootstrapped || !first.Bootstrap.RemoteControlEnabled {
		t.Fatalf("first start output = %#v", first)
	}

	second, err := runner.EnsureRemoteControlStarted()
	if err != nil {
		t.Fatalf("EnsureRemoteControlStarted second error = %v", err)
	}
	if second.Start == nil || second.Start.Status != StatusAlreadyRunning {
		t.Fatalf("second start output = %#v", second)
	}

	settings, err := LoadSettings(filepath.Join(home, StateDirName, SettingsFileName))
	if err != nil {
		t.Fatalf("LoadSettings error = %v", err)
	}
	if !settings.RemoteControlEnabled {
		t.Fatal("remote control setting not persisted")
	}
}

func TestLifecycleRunnerWaitUntilReadyIncludesStderrTail(t *testing.T) {
	daemon := NewDaemonForCodexHome(t.TempDir(), "codex-go-test")
	logFile := StderrLogPathForPIDFile(daemon.Paths.PIDFile)
	if err := os.MkdirAll(filepath.Dir(logFile), 0o700); err != nil {
		t.Fatalf("MkdirAll stderr log dir error = %v", err)
	}
	if err := os.WriteFile(logFile, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatalf("WriteFile stderr log error = %v", err)
	}

	oldProbe := probeAppServerVersionOnSocket
	oldTimeout := lifecycleReadyTimeout
	oldRetry := lifecycleReadyRetry
	probeAppServerVersionOnSocket = func(path string, timeout time.Duration) (string, error) {
		if path != daemon.Paths.SocketPath {
			t.Fatalf("probe path = %q, want %q", path, daemon.Paths.SocketPath)
		}
		return "", errors.New("socket not ready")
	}
	lifecycleReadyTimeout = time.Millisecond
	lifecycleReadyRetry = time.Millisecond
	t.Cleanup(func() {
		probeAppServerVersionOnSocket = oldProbe
		lifecycleReadyTimeout = oldTimeout
		lifecycleReadyRetry = oldRetry
	})

	_, err := NewLifecycleRunner(daemon).waitUntilReady()
	if err == nil {
		t.Fatal("waitUntilReady error = nil")
	}
	got := err.Error()
	for _, want := range []string{
		"socket not ready",
		"app server did not become ready on " + daemon.Paths.SocketPath,
		"Daemon used app-server:",
		"  path: " + daemon.Paths.ManagedCodexBin,
		"  version: unknown",
		"Managed app-server stderr (" + logFile + "):",
		"  first",
		"  second",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("waitUntilReady error missing %q:\n%s", want, got)
		}
	}
}

func fixedDaemonTime() time.Time {
	return time.Date(2026, 6, 30, 2, 3, 4, 0, time.UTC)
}

type lifecycleManagedDaemonStub struct {
	appRunning     bool
	updaterRunning bool
	socketReady    bool
	nextPID        uint32
	appVersion     string
}

func stubLifecycleManagedDaemon(t *testing.T) *lifecycleManagedDaemonStub {
	t.Helper()
	stub := &lifecycleManagedDaemonStub{nextPID: 4100, appVersion: "9.9.9"}
	oldEnsure := ensureManagedCodexBin
	oldIsRunning := pidBackendIsStartingOrRunning
	oldStart := startPIDBackend
	oldStop := stopPIDBackend
	oldProbe := probeAppServerVersionOnSocket
	oldEnable := enableRemoteControlOnSocket
	oldDisable := disableRemoteControlOnSocket
	ensureManagedCodexBin = func(string) error { return nil }
	pidBackendIsStartingOrRunning = func(backend *PIDBackend) (bool, error) {
		if backend != nil && backend.CommandKind == PIDCommandUpdateLoop {
			return stub.updaterRunning, nil
		}
		return stub.appRunning, nil
	}
	startPIDBackend = func(backend *PIDBackend) (*uint32, error) {
		if backend != nil && backend.CommandKind == PIDCommandUpdateLoop {
			stub.updaterRunning = true
			return nil, nil
		}
		stub.appRunning = true
		stub.socketReady = true
		stub.nextPID++
		pid := stub.nextPID
		return &pid, nil
	}
	stopPIDBackend = func(backend *PIDBackend) error {
		if backend != nil && backend.CommandKind == PIDCommandUpdateLoop {
			stub.updaterRunning = false
			return nil
		}
		stub.appRunning = false
		stub.socketReady = false
		return nil
	}
	probeAppServerVersionOnSocket = func(string, time.Duration) (string, error) {
		if stub.socketReady {
			return stub.appVersion, nil
		}
		return "", errors.New("app server is not ready")
	}
	enableRemoteControlOnSocket = func(string, time.Duration, time.Duration) (RemoteControlReadyStatus, error) {
		return RemoteControlReadyStatus{}, nil
	}
	disableRemoteControlOnSocket = func(string, time.Duration, time.Duration) (RemoteControlReadyStatus, error) {
		return RemoteControlReadyStatus{}, nil
	}
	t.Cleanup(func() {
		ensureManagedCodexBin = oldEnsure
		pidBackendIsStartingOrRunning = oldIsRunning
		startPIDBackend = oldStart
		stopPIDBackend = oldStop
		probeAppServerVersionOnSocket = oldProbe
		enableRemoteControlOnSocket = oldEnable
		disableRemoteControlOnSocket = oldDisable
	})
	return stub
}

func stubRemoteControlSocketCalls(
	t *testing.T,
	probe func(string, time.Duration) (string, error),
	enable func(string, time.Duration, time.Duration) (RemoteControlReadyStatus, error),
	disable func(string, time.Duration, time.Duration) (RemoteControlReadyStatus, error),
) {
	t.Helper()
	oldProbe := probeAppServerVersionOnSocket
	oldEnable := enableRemoteControlOnSocket
	oldDisable := disableRemoteControlOnSocket
	probeAppServerVersionOnSocket = probe
	enableRemoteControlOnSocket = enable
	disableRemoteControlOnSocket = disable
	t.Cleanup(func() {
		probeAppServerVersionOnSocket = oldProbe
		enableRemoteControlOnSocket = oldEnable
		disableRemoteControlOnSocket = oldDisable
	})
}
