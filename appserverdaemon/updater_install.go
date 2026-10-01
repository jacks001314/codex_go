package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"codex_go/install"
)

// Managed updater eligibility and manual package updates (Rust
// managed_install::{is_stable_standalone_release, supports_daemon_update_loop},
// Daemon::ensure_managed_updater, prepare_install::update_from_cli).
//
// A package a user selected with `daemon update --from-cli` is pinned: its
// release directory and its missing `auto-update-version` marker both fail the
// stable-release test, so the managed updater is stopped and never replaces it.

// standaloneReleaseTargets are the release directory suffixes an installer
// publishes (Rust is_stable_standalone_release).
var standaloneReleaseTargets = []string{
	"aarch64-apple-darwin",
	"x86_64-apple-darwin",
	"aarch64-unknown-linux-gnu",
	"x86_64-unknown-linux-gnu",
	"aarch64-unknown-linux-musl",
	"x86_64-unknown-linux-musl",
	"aarch64-pc-windows-msvc",
	"x86_64-pc-windows-msvc",
}

// UpdateStatus is the outcome of a manual daemon package update
// (Rust app-server-daemon::UpdateStatus).
type UpdateStatus string

const (
	UpdateUpdated     UpdateStatus = "updated"
	UpdateNoUpdate    UpdateStatus = "noUpdate"
	UpdateUnsupported UpdateStatus = "unsupported"
)

// UpdateOutput is what `codex app-server daemon update` prints
// (Rust app-server-daemon::UpdateOutput).
type UpdateOutput struct {
	Status           UpdateStatus `json:"status"`
	ManagedCodexPath string       `json:"managedCodexPath"`
	InstalledVersion *string      `json:"installedVersion,omitempty"`
	RunningVersion   *string      `json:"runningVersion,omitempty"`
	Message          string       `json:"message"`
}

// managedCodexVersionStrict is injectable so package tests can pin the version a
// staged executable reports without running it.
var managedCodexVersionStrict = managedCodexVersion

// UpdateFromCLI copies and pins this CLI's package as the managed daemon
// (Rust app-server-daemon::update_from_cli). It returns nil when the caller
// cancels without changing the installation.
func UpdateFromCLI(codexHome string, confirm func(*DaemonInstallRequest) (bool, error)) (*UpdateOutput, error) {
	if err := EnsureSupportedPlatform(); err != nil {
		return nil, err
	}
	if err := EnsureNonElevated(); err != nil {
		return nil, err
	}
	daemon := NewDaemonForCodexHome(codexHome, "")
	daemon.refreshInstallation()
	settings, err := daemon.LoadSettings()
	if err != nil {
		return nil, err
	}
	var confirmer installConfirmer
	if confirm != nil {
		confirmer = func(request *DaemonInstallRequest) (bool, error) { return confirm(request) }
	}
	if err := prepareDaemonPackage(daemon, settings, daemonInstallReplace, confirmer); err != nil {
		if errors.Is(err, errDaemonInstallCancelled) {
			return nil, nil
		}
		return nil, err
	}
	managedCodexPath := ManagedCodexBin(codexHome)
	installedVersion, err := managedCodexVersionStrict(managedCodexPath)
	if err != nil {
		return nil, err
	}
	var runningVersion *string
	if version, err := ProbeAppServerVersionOnSocket(daemon.Paths.SocketPath, ControlSocketProbeTimeout); err == nil {
		runningVersion = &version
	}
	return &UpdateOutput{
		Status:           UpdateUpdated,
		ManagedCodexPath: managedCodexPath,
		InstalledVersion: &installedVersion,
		RunningVersion:   runningVersion,
		Message:          "The CLI package is selected and pinned. Run `codex app-server daemon update` to return to production updates.",
	}, nil
}

// isStableStandaloneRelease reports whether the selected release is one an
// installer published and chose to follow (Rust
// managed_install::is_stable_standalone_release).
func isStableStandaloneRelease(home string, codexBin string) bool {
	root := PackageRoot(home)
	releases := canonicalPath(filepath.Join(root, releasesDirName))
	release := canonicalPath(filepath.Join(root, "current"))
	if releases == "" || release == "" || filepath.Dir(release) != releases {
		return false
	}
	name := filepath.Base(release)
	// GNU packages can seed the new directory; retain legacy updater eligibility.
	if filepath.Base(root) == legacyPackagesDirname && strings.HasSuffix(name, "-gnu") {
		return false
	}
	version := ""
	for _, target := range standaloneReleaseTargets {
		if strings.HasSuffix(name, "-"+target) {
			version = strings.TrimSuffix(name, "-"+target)
			break
		}
	}
	if !isReleaseVersion(version) {
		return false
	}
	marker, err := os.ReadFile(filepath.Join(root, autoUpdateVersionFileName))
	if err != nil || strings.TrimSpace(string(marker)) != name {
		return false
	}
	return pathWithin(canonicalPath(codexBin), release)
}

// isReleaseVersion reports whether value is a plain x.y.z release.
func isReleaseVersion(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

// supportsDaemonUpdateLoop probes the managed binary's internal updater command
// without running a long-lived process (Rust
// managed_install::supports_daemon_update_loop).
func supportsDaemonUpdateLoop(codexBin string) bool {
	return supportsDaemonCommand(codexBin, []string{"pid-update-loop", "--help"})
}

// supportsDaemonCommand probes an internal daemon command of a managed binary
// without running a long-lived process (Rust
// managed_install::supports_daemon_command).
func supportsDaemonCommand(codexBin string, args []string) bool {
	command := exec.Command(codexBin, append([]string{"app-server", "daemon"}, args...)...)
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	done := make(chan error, 1)
	if err := command.Start(); err != nil {
		return false
	}
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(updaterProbeTimeout):
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		<-done
		return false
	}
}

// updaterProbeTimeout bounds the internal updater probe.
const updaterProbeTimeout = 5 * time.Second

// ensureManagedUpdater makes the managed updater match the selected package and
// reports whether it should run (Rust Daemon::ensure_managed_updater).
func ensureManagedUpdater(daemon *Daemon, settings *DaemonSettings) (bool, error) {
	updater := NewPIDUpdateLoopBackend(daemon.BackendPaths(settings))
	stopUpdater := func() error {
		return stopPIDBackend(updater, settings.ShutdownGraceSecondsValue())
	}
	if !settings.AutoUpdateEnabled() {
		return false, stopUpdater()
	}
	home := daemonCodexHome(daemon)
	managedBin := daemon.Paths.ManagedCodexBin
	// An installer publishes `current` and the latest marker separately, so keep
	// its updater alive while that publication may be in progress.
	latestSelection := isRegularFile(filepath.Join(PackageRoot(home), autoUpdateVersionFileName))
	pinned := func() (bool, error) {
		if !latestSelection {
			return false, stopUpdater()
		}
		return false, nil
	}
	if !isStableStandaloneRelease(home, managedBin) {
		return pinned()
	}
	resolved, err := resolveFinalPath(managedBin)
	if err != nil {
		return pinned()
	}
	if !supportsDaemonUpdateLoop(resolved) || !isStableStandaloneRelease(home, managedBin) ||
		canonicalPath(managedBin) != resolved {
		return pinned()
	}
	pid, err := startPIDBackend(NewPIDUpdateLoopBackend(BackendPaths{
		CodexBin:      resolved,
		UpdatePIDFile: daemon.Paths.UpdatePIDFile,
	}))
	if err != nil {
		return false, err
	}
	_ = pid
	return true, nil
}

// stopDaemonUpdater stops the managed updater (Rust PidBackend::stop).
func stopDaemonUpdater(daemon *Daemon, settings *DaemonSettings) error {
	return stopPIDBackend(NewPIDUpdateLoopBackend(daemon.BackendPaths(settings)), settings.ShutdownGraceSecondsValue())
}

// startDaemonUpdater starts the managed updater again.
func startDaemonUpdater(daemon *Daemon, settings *DaemonSettings) error {
	_, err := startPIDBackend(NewPIDUpdateLoopBackend(daemon.BackendPaths(settings)))
	return err
}

// waitForAppServerReady blocks until the selected package answers, mirroring
// Rust Daemon::wait_until_ready.
func waitForAppServerReady(daemon *Daemon) error {
	deadline := time.Now().Add(lifecycleReadyTimeout)
	var lastErr error
	for {
		_, err := probeAppServerVersionOnSocket(daemonSocketPath(daemon), ControlSocketProbeTimeout)
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			managedVersion := managedCodexVersionBestEffort(daemon.Paths.ManagedCodexBin)
			tail, _ := readStderrLogTail(daemon.Paths.PIDFile)
			return fmt.Errorf("%w: %s", lastErr, daemon.AppServerNotReadyContext(managedVersion, tail))
		}
		time.Sleep(lifecycleReadyRetry)
	}
}

// managedCodexVersion reports the version of a managed executable, failing when
// it cannot be read (Rust managed_install::managed_codex_version).
func managedCodexVersion(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("managed Codex binary path is empty")
	}
	output, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", err
	}
	return install.ParseCodexVersion(string(output))
}
