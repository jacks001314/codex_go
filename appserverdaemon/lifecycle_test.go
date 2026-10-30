package appserverdaemon

import (
	"encoding/json"
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

func writeDaemonSettingsForTest(t *testing.T, daemon *Daemon, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(daemon.Paths.SettingsFile), 0o700); err != nil {
		t.Fatalf("MkdirAll settings dir error = %v", err)
	}
	if err := os.WriteFile(daemon.Paths.SettingsFile, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile settings error = %v", err)
	}
}

func firstEventIndex(events []string, name string) int {
	for i, event := range events {
		if event == name {
			return i
		}
	}
	return -1
}

// TestLifecycleRunnerBootstrapPreservesUpdaterPreferencesLikeRust mirrors Rust
// #43542: bootstrap loads the stored settings, replaces only the remote-control
// preference, and never launches a disabled updater even transiently.
func TestLifecycleRunnerBootstrapPreservesUpdaterPreferencesLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	home := privateDaemonTestHome(t)
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	writeDaemonSettingsForTest(t, daemon, `{"remoteControlEnabled":false,"featureOverrides":{"code_mode_host":true},"shutdownGraceSeconds":15,"updater":{"autoUpdateEnabled":false,"updateIntervalMinutes":17},"futureSetting":42}`)

	output, err := NewLifecycleRunner(daemon).Bootstrap(&BootstrapOptions{RemoteControlEnabled: true})
	if err != nil {
		t.Fatalf("Bootstrap error = %v", err)
	}
	if output.AutoUpdateEnabled {
		t.Fatal("bootstrap reported automatic updates enabled for a disabled preference")
	}
	if stub.updaterStarts != 0 {
		t.Fatalf("updater launches = %d, want none while automatic updates are disabled", stub.updaterStarts)
	}
	settings, err := LoadSettings(daemon.Paths.SettingsFile)
	if err != nil {
		t.Fatalf("LoadSettings error = %v", err)
	}
	if !settings.RemoteControlEnabled {
		t.Fatal("bootstrap did not persist the launch remote-control preference")
	}
	if settings.AutoUpdateEnabled() {
		t.Fatal("bootstrap lost the stored autoUpdateEnabled=false")
	}
	if got := settings.UpdateInterval(); got != 17*time.Minute {
		t.Fatalf("stored update interval = %v, want 17m", got)
	}
	if got := settings.ShutdownGraceSecondsValue(); got != 15 {
		t.Fatalf("stored shutdown grace = %d, want 15", got)
	}
	if !FeatureOverridesEqual(settings.FeatureOverrides, map[string]bool{"code_mode_host": true}) {
		t.Fatalf("stored feature overrides = %#v", settings.FeatureOverrides)
	}
	data, err := os.ReadFile(daemon.Paths.SettingsFile)
	if err != nil {
		t.Fatalf("ReadFile settings error = %v", err)
	}
	values := map[string]any{}
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatalf("parse settings error = %v", err)
	}
	if values["futureSetting"] != float64(42) {
		t.Fatalf("unknown setting = %#v, want preserved", values["futureSetting"])
	}
}

// TestLifecycleRunnerRestartStopsUpdaterBeforeRestartWhenDisabledLikeRust pins
// Rust #43542: a disabled preference retires the updater before the app-server
// restart can replace the selected package.
func TestLifecycleRunnerRestartStopsUpdaterBeforeRestartWhenDisabledLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	home := privateDaemonTestHome(t)
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	writeDaemonSettingsForTest(t, daemon, `{"updater":{"autoUpdateEnabled":false}}`)
	stub.appRunning = true
	stub.socketReady = true
	stub.updaterRunning = true

	output, err := NewLifecycleRunner(daemon).Run(LifecycleRestart)
	if err != nil {
		t.Fatalf("Run(restart) error = %v", err)
	}
	if output.Status != StatusRestarted || stub.appStops != 1 {
		t.Fatalf("restart output = %#v, appStops = %d", output, stub.appStops)
	}
	if stub.updaterRunning {
		t.Fatal("restart left the updater running with automatic updates disabled")
	}
	stopUpdater := firstEventIndex(stub.events, "stop-updater")
	stopApp := firstEventIndex(stub.events, "stop-app")
	if stopUpdater < 0 || stopApp < 0 || stopUpdater > stopApp {
		t.Fatalf("events = %v, want the updater stopped before the app-server", stub.events)
	}
}

// TestLifecycleRunnerStartStopsUpdaterWhenDisabledLikeRust pins that reusing a
// running server still applies the saved disabled preference (Rust Daemon::start
// ensures the updater for every backend).
func TestLifecycleRunnerStartStopsUpdaterWhenDisabledLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	home := privateDaemonTestHome(t)
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	writeDaemonSettingsForTest(t, daemon, `{"updater":{"autoUpdateEnabled":false}}`)
	stub.appRunning = true
	stub.socketReady = true
	stub.updaterRunning = true

	output, err := NewLifecycleRunner(daemon).Run(LifecycleStart)
	if err != nil {
		t.Fatalf("Run(start) error = %v", err)
	}
	if output.Status != StatusAlreadyRunning {
		t.Fatalf("start output = %#v, want already running", output)
	}
	if stub.updaterRunning {
		t.Fatal("start left the updater running with automatic updates disabled")
	}
}

// TestLifecycleRunnerSetRemoteControlStopsUpdaterWhenDisabledLikeRust pins that
// a remote-control restart applies the saved updater preference (Rust
// Daemon::set_remote_control_locked).
func TestLifecycleRunnerSetRemoteControlStopsUpdaterWhenDisabledLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	home := privateDaemonTestHome(t)
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	writeDaemonSettingsForTest(t, daemon, `{"remoteControlEnabled":false,"updater":{"autoUpdateEnabled":false}}`)
	stub.appRunning = true
	stub.socketReady = true
	stub.updaterRunning = true

	output, err := NewLifecycleRunner(daemon).SetRemoteControl(RemoteControlEnabled)
	if err != nil {
		t.Fatalf("SetRemoteControl error = %v", err)
	}
	if output.Status != RemoteStatusEnabled || !output.RemoteControlEnabled {
		t.Fatalf("remote-control output = %#v", output)
	}
	if stub.updaterRunning {
		t.Fatal("remote-control restart left the updater running with automatic updates disabled")
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
	// appStops counts app-server backend stops so a test can tell a restart
	// (stop + start) from a reused daemon.
	appStops int
	// updaterStarts counts updater launches; a disabled preference must not
	// start one even transiently.
	updaterStarts int
	// events records backend transitions in order for preference-ordering tests.
	events     []string
	nextPID    uint32
	appVersion string
}

func stubLifecycleManagedDaemon(t *testing.T) *lifecycleManagedDaemonStub {
	t.Helper()
	stub := &lifecycleManagedDaemonStub{nextPID: 4100, appVersion: "9.9.9"}
	oldEnsure := ensureManagedCodexBin
	oldPrepare := prepareManagedDaemon
	oldIsRunning := pidBackendIsStartingOrRunning
	oldStart := startPIDBackend
	oldStop := stopPIDBackend
	oldProbe := probeAppServerVersionOnSocket
	oldEnable := enableRemoteControlOnSocket
	oldDisable := disableRemoteControlOnSocket
	oldDetachedProbe := ensureDetachedLaunchProbe
	ensureManagedCodexBin = func(string) error { return nil }
	// The detached-launch preflight spawns the managed binary, which the tests
	// replace with a placeholder file, so it is stubbed like the other edges.
	ensureDetachedLaunchProbe = func(string) error { return nil }
	// These tests exercise the lifecycle around an already-installed daemon, so
	// the package preparation step is stubbed exactly like the managed-binary
	// check (Rust's lifecycle tests patch prepare_install::prepare).
	prepareManagedDaemon = func(*Daemon, *DaemonSettings) error { return nil }
	pidBackendIsStartingOrRunning = func(backend *PIDBackend) (bool, error) {
		if backend != nil && backend.CommandKind == PIDCommandUpdateLoop {
			return stub.updaterRunning, nil
		}
		return stub.appRunning, nil
	}
	startPIDBackend = func(backend *PIDBackend) (*uint32, error) {
		if backend != nil && backend.CommandKind == PIDCommandUpdateLoop {
			stub.updaterRunning = true
			stub.updaterStarts++
			stub.events = append(stub.events, "start-updater")
			return nil, nil
		}
		stub.appRunning = true
		stub.socketReady = true
		stub.events = append(stub.events, "start-app")
		stub.nextPID++
		pid := stub.nextPID
		return &pid, nil
	}
	stopPIDBackend = func(backend *PIDBackend, graceSeconds int) error {
		if backend != nil && backend.CommandKind == PIDCommandUpdateLoop {
			stub.updaterRunning = false
			stub.events = append(stub.events, "stop-updater")
			return nil
		}
		if stub.appRunning {
			stub.appStops++
		}
		stub.appRunning = false
		stub.socketReady = false
		stub.events = append(stub.events, "stop-app")
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
		prepareManagedDaemon = oldPrepare
		pidBackendIsStartingOrRunning = oldIsRunning
		startPIDBackend = oldStart
		stopPIDBackend = oldStop
		probeAppServerVersionOnSocket = oldProbe
		enableRemoteControlOnSocket = oldEnable
		disableRemoteControlOnSocket = oldDisable
		ensureDetachedLaunchProbe = oldDetachedProbe
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

// TestLifecycleRunnerStartWithFeaturesLikeRust mirrors Rust
// app-server-daemon::start_with_features: the launch's features are merged with
// the stored overrides and persisted, and a plain start clears them.
func TestLifecycleRunnerStartWithFeaturesLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	runner := NewLifecycleRunner(daemon)

	if _, err := runner.StartWithFeatures(map[string]bool{"code_mode_host": true}); err != nil {
		t.Fatalf("StartWithFeatures error = %v", err)
	}
	if !stub.appRunning {
		t.Fatal("StartWithFeatures did not start the backend")
	}
	settings, err := LoadSettings(daemon.Paths.SettingsFile)
	if err != nil {
		t.Fatalf("LoadSettings error = %v", err)
	}
	if !FeatureOverridesEqual(settings.FeatureOverrides, map[string]bool{"code_mode_host": true}) {
		t.Fatalf("stored overrides = %#v", settings.FeatureOverrides)
	}

	// Only a fresh launch may replace the stored overrides; a launch that reuses
	// the running server leaves them alone (Rust Daemon::start).
	if _, err := runner.StartWithFeatures(map[string]bool{"auth_elicitation": true}); err != nil {
		t.Fatalf("second StartWithFeatures error = %v", err)
	}
	settings, err = LoadSettings(daemon.Paths.SettingsFile)
	if err != nil {
		t.Fatalf("LoadSettings error = %v", err)
	}
	if !FeatureOverridesEqual(settings.FeatureOverrides, map[string]bool{"code_mode_host": true}) {
		t.Fatalf("stored overrides after reuse = %#v", settings.FeatureOverrides)
	}
}

// TestLifecycleRunnerRestartWithFeaturesLikeRust mirrors Rust
// restart_with_features: changed features restart the daemon and persist, while
// an unchanged set only re-checks the running server.
func TestLifecycleRunnerRestartWithFeaturesLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	if _, err := runner.StartWithFeatures(map[string]bool{"code_mode_host": true}); err != nil {
		t.Fatalf("StartWithFeatures error = %v", err)
	}

	// Without a running daemon the restart is refused like Rust's
	// "no running managed daemon" guard.
	stub.appRunning = false
	stub.socketReady = false
	if _, err := runner.RestartWithFeatures(map[string]bool{"code_mode_host": true}); err == nil ||
		!strings.Contains(err.Error(), "no running managed daemon") {
		t.Fatalf("RestartWithFeatures without a daemon error = %v", err)
	}

	// A changed set restarts the backend and persists the merged overrides.
	stub.appRunning = true
	stub.socketReady = true
	stopsBefore := stub.appStops
	if _, err := runner.RestartWithFeatures(map[string]bool{"auth_elicitation": true}); err != nil {
		t.Fatalf("RestartWithFeatures error = %v", err)
	}
	if stub.appStops <= stopsBefore {
		t.Fatalf("restarts = %d, want more than %d", stub.appStops, stopsBefore)
	}
	settings, err := LoadSettings(daemon.Paths.SettingsFile)
	if err != nil {
		t.Fatalf("LoadSettings error = %v", err)
	}
	want := map[string]bool{"code_mode_host": true, "auth_elicitation": true}
	if !FeatureOverridesEqual(settings.FeatureOverrides, want) {
		t.Fatalf("stored overrides = %#v, want %#v", settings.FeatureOverrides, want)
	}

	// An unchanged set does not restart the backend.
	stopsBefore = stub.appStops
	if _, err := runner.RestartWithFeatures(map[string]bool{"auth_elicitation": true}); err != nil {
		t.Fatalf("unchanged RestartWithFeatures error = %v", err)
	}
	if stub.appStops != stopsBefore {
		t.Fatalf("restarts = %d, want %d (unchanged features must not restart)", stub.appStops, stopsBefore)
	}
}

func TestLifecycleRunnerRestartApplyingFeaturesLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	if err := SaveSettings(daemon.Paths.SettingsFile, &DaemonSettings{FeatureOverrides: map[string]bool{
		"connectors":              true,
		"apps":                    true,
		"auth_elicitation":        true,
		"api_key_model_discovery": false,
	}}); err != nil {
		t.Fatalf("SaveSettings() error = %v", err)
	}
	stub.appRunning = false
	stub.socketReady = false

	if _, err := runner.RestartApplyingFeatures(map[string]bool{
		"apps":                    false,
		"api_key_model_discovery": true,
	}); err != nil {
		t.Fatalf("RestartApplyingFeatures() error = %v", err)
	}
	if !stub.appRunning {
		t.Fatal("explicit restart did not start the managed backend")
	}
	settings, err := LoadSettings(daemon.Paths.SettingsFile)
	if err != nil {
		t.Fatalf("LoadSettings() error = %v", err)
	}
	want := map[string]bool{
		"apps":                    false,
		"auth_elicitation":        true,
		"api_key_model_discovery": true,
	}
	if !FeatureOverridesEqual(settings.FeatureOverrides, want) {
		t.Fatalf("stored overrides = %#v, want %#v", settings.FeatureOverrides, want)
	}

	stopsBefore := stub.appStops
	if _, err := runner.RestartApplyingFeatures(map[string]bool{"apps": false}); err != nil {
		t.Fatalf("unchanged RestartApplyingFeatures() error = %v", err)
	}
	if stub.appStops <= stopsBefore {
		t.Fatalf("explicit restart reused the daemon: stops = %d, want more than %d", stub.appStops, stopsBefore)
	}
}
