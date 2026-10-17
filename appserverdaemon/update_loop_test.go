package appserverdaemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/install"
)

// TestUpdateOnceRestoreUsesReleaseNameLikeRust pins that the restore production
// trigger compares the release *name* (Rust InstallerMode::Update/RestoreProduction
// take `previous_release`), not the release path, and that the installer
// receives the name as its previous release.
func TestUpdateOnceRestoreUsesReleaseNameLikeRust(t *testing.T) {
	stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	root, _ := publishStableSelection(t, home, "1.0.0")
	pinnedName := "local-deadbeef-" + platformTarget()
	pinned := filepath.Join(root, releasesDirName, pinnedName)
	if err := os.MkdirAll(filepath.Join(pinned, "bin"), 0o700); err != nil {
		t.Fatalf("MkdirAll pinned error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(pinned, "bin", managedCodexFileName()), []byte("pinned"), 0o700); err != nil {
		t.Fatalf("WriteFile pinned entrypoint error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, autoUpdateVersionFileName)); err != nil {
		t.Fatalf("Remove marker error = %v", err)
	}
	if err := selectDaemonRelease(root, pinned); err != nil {
		t.Fatalf("selectDaemonRelease(pinned) error = %v", err)
	}
	daemon.refreshInstallation()
	runner := NewLifecycleRunner(daemon)
	runningIdentity := install.ExecutableIdentityFromBytes([]byte("old"))
	var installed installerMode
	options := &UpdateLoopOptions{
		Install: func(_ context.Context, mode installerMode, _ string) error {
			installed = mode
			return nil
		},
		ReadFile:      os.ReadFile,
		ReexecUpdater: func(string) error { return nil },
	}

	// A restore authorized for a different release name is refused before the
	// installer runs.
	if _, _, _, err := updateOnce(context.Background(), runner, &runningIdentity, options, UpdateTrigger{Kind: UpdateTriggerRestoreProduction, Release: "1.0.0-" + platformTarget()}); err == nil ||
		!strings.Contains(err.Error(), "daemon selection changed") {
		t.Fatalf("mismatched restore error = %v", err)
	}
	if installed.Kind != installerUpdate {
		t.Fatalf("installer ran for a mismatched restore: %#v", installed)
	}

	// The matching release name reaches the installer in restore mode; the
	// selection is still pinned afterwards, so the post-install check reports it.
	if _, _, _, err := updateOnce(context.Background(), runner, &runningIdentity, options, UpdateTrigger{Kind: UpdateTriggerRestoreProduction, Release: pinnedName}); err == nil ||
		!strings.Contains(err.Error(), "did not select a stable latest release") {
		t.Fatalf("matching restore error = %v", err)
	}
	if installed.Kind != installerRestoreProduction || installed.Release != pinnedName {
		t.Fatalf("installer mode = %#v, want restore of %q", installed, pinnedName)
	}
}

func TestUpdateModesForIdentities(t *testing.T) {
	same := ExecutableIdentityFromBytes([]byte("same"))
	runningChanged := ExecutableIdentityFromBytes([]byte("old"))
	managedChanged := ExecutableIdentityFromBytes([]byte("new"))

	mode, refresh := UpdateModesForIdentities(&same, &same)
	if mode != RestartIfVersionChanged || refresh != UpdaterRefreshNone {
		t.Fatalf("same identity modes = %s, %s", mode, refresh)
	}

	mode, refresh = UpdateModesForIdentities(&runningChanged, &managedChanged)
	if mode != RestartAlways || refresh != UpdaterRefreshReexecIfManagedBinaryChanged {
		t.Fatalf("changed identity modes = %s, %s", mode, refresh)
	}
}

func TestShouldReexecUpdater(t *testing.T) {
	if !ShouldReexecUpdater(UpdaterRefreshReexecIfManagedBinaryChanged, RestartRestarted) {
		t.Fatal("changed updater with restarted app-server should reexec")
	}
	for _, outcome := range []RestartIfRunningOutcome{RestartBusy, RestartNotRunning, RestartNotReady, RestartAlreadyCurrent} {
		if ShouldReexecUpdater(UpdaterRefreshReexecIfManagedBinaryChanged, outcome) {
			t.Fatalf("outcome %s unexpectedly reexecs updater", outcome)
		}
	}
	if ShouldReexecUpdater(UpdaterRefreshNone, RestartRestarted) {
		t.Fatal("refresh none unexpectedly reexecs updater")
	}
}

func TestCurrentUpdaterIdentityUsesExecutableBytes(t *testing.T) {
	identityOld := install.ExecutableIdentityFromBytes([]byte("old"))
	identityNew := install.ExecutableIdentityFromBytes([]byte("new"))
	options := &UpdateLoopOptions{
		CurrentExe: func() (string, error) { return "codex-old", nil },
		ReadFile: func(path string) ([]byte, error) {
			if path != "codex-old" {
				t.Fatalf("ReadFile path = %q", path)
			}
			return []byte("old"), nil
		},
	}
	identity, err := CurrentUpdaterIdentity(options)
	if err != nil {
		t.Fatalf("CurrentUpdaterIdentity error = %v", err)
	}
	// #50559: the updater identity hashes the bytes *and* the canonical path, so
	// two different executables with the same bytes stay distinguishable.
	if identity == nil || identity.Digest != identityOld.Digest || identity.Digest == identityNew.Digest {
		t.Fatalf("identity = %#v, want digest %#v", identity, identityOld)
	}
	if identity.PathDigest == "" {
		t.Fatalf("identity = %#v, want a path digest", identity)
	}
}

func TestUpdateOnceRestartsWhenUpdaterIdentityChanged(t *testing.T) {
	stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	managedBin := daemon.Paths.ManagedCodexBin
	// The updater only restarts a daemon whose selected release is still the
	// latest-channel one, so publish an installer-shaped selection first.
	root := filepath.Join(home, "packages", daemonPackagesDirname)
	release := filepath.Join(root, releasesDirName, "1.2.3-"+platformTarget())
	if err := os.MkdirAll(filepath.Join(release, "bin"), 0o700); err != nil {
		t.Fatalf("MkdirAll release error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(release, "bin", managedCodexFileName()), []byte("new"), 0o700); err != nil {
		t.Fatalf("WriteFile release entrypoint error = %v", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll package root error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, autoUpdateVersionFileName), []byte("1.2.3-"+platformTarget()), 0o600); err != nil {
		t.Fatalf("WriteFile marker error = %v", err)
	}
	if err := selectDaemonRelease(root, release); err != nil {
		t.Fatalf("selectDaemonRelease error = %v", err)
	}
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	if _, err := runner.Run(LifecycleStart); err != nil {
		t.Fatalf("Run(start) error = %v", err)
	}
	options := &UpdateLoopOptions{
		Install:  func(context.Context, installerMode, string) error { return nil },
		ReadFile: os.ReadFile,
		ReexecUpdater: func(path string) error {
			if path != managedBin {
				t.Fatalf("reexec path = %q, want %q", path, managedBin)
			}
			return nil
		},
	}
	runningIdentity := install.ExecutableIdentityFromBytes([]byte("old"))
	control, err := UpdateOnce(context.Background(), runner, &runningIdentity, options)
	if err != nil {
		t.Fatalf("UpdateOnce error = %v", err)
	}
	if control != UpdateLoopStop {
		t.Fatalf("control = %s", control)
	}
}

// TestRestartModeForTriggerLikeRust pins the #50559 restart-mode selection:
// manual updates and production restores no longer force a restart on a release
// change, and an adopted updater only restarts when its identity differs.
func TestRestartModeForTriggerLikeRust(t *testing.T) {
	same := install.ExecutableIdentityFromBytes([]byte("same"))
	changed := install.ExecutableIdentityFromBytes([]byte("changed"))
	for _, trigger := range []UpdateTrigger{
		{Kind: UpdateTriggerManual},
		{Kind: UpdateTriggerRestoreProduction, Release: "local-deadbeef-" + platformTarget()},
	} {
		if got := restartModeForTrigger(trigger, changed, same); got != RestartIfBinaryOrVersionChanged {
			t.Fatalf("restartModeForTrigger(%v) = %s, want IfBinaryOrVersionChanged", trigger, got)
		}
	}
	if got := restartModeForTrigger(UpdateTrigger{Kind: UpdateTriggerScheduled}, changed, same); got != RestartIfBinaryOrVersionChanged {
		t.Fatalf("scheduled restart mode = %s, want IfBinaryOrVersionChanged", got)
	}
	if got := restartModeForTrigger(UpdateTrigger{Kind: UpdateTriggerScheduled}, same, same); got != RestartIfVersionChanged {
		t.Fatalf("scheduled restart mode = %s, want IfVersionChanged", got)
	}
}

// TestTryRestartIfRunningRestartsForSameContentsDifferentPath pins that the
// daemon restart decision compares the whole identity, so a resource-only
// release with identical executable bytes still replaces the process (#50559).
func TestTryRestartIfRunningRestartsForSameContentsDifferentPath(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	stub.appRunning = true
	stub.socketReady = true
	stub.appVersion = "1.0.0"
	home := t.TempDir()
	root, _ := publishStableSelection(t, home, "1.0.0")
	daemon := NewDaemonForCodexHome(home, "")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	firstBin := daemon.Paths.ManagedCodexBin
	runningIdentity, err := install.ExecutableIdentityFromFile(firstBin)
	if err != nil {
		t.Fatalf("ExecutableIdentityFromFile(first) error = %v", err)
	}
	startTime, err := readPIDProcessStartTime(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("readPIDProcessStartTime error = %v", err)
	}
	if err := WritePIDRecord(daemon.Paths.PIDFile, &PIDRecord{
		PID:                uint32(os.Getpid()),
		ProcessStartTime:   startTime,
		ExecutableIdentity: &runningIdentity,
	}); err != nil {
		t.Fatalf("WritePIDRecord(app-server) error = %v", err)
	}
	contents, err := os.ReadFile(firstBin)
	if err != nil {
		t.Fatalf("ReadFile(first) error = %v", err)
	}
	nextName := "1.0.1-" + platformTarget()
	next := filepath.Join(root, releasesDirName, nextName)
	if err := os.MkdirAll(filepath.Join(next, "bin"), 0o700); err != nil {
		t.Fatalf("MkdirAll(next release) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(next, "bin", managedCodexFileName()), contents, 0o700); err != nil {
		t.Fatalf("WriteFile(next entrypoint) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, autoUpdateVersionFileName), []byte(nextName), 0o600); err != nil {
		t.Fatalf("WriteFile(next marker) error = %v", err)
	}
	if err := selectDaemonRelease(root, next); err != nil {
		t.Fatalf("selectDaemonRelease(next) error = %v", err)
	}
	daemon.refreshInstallation()
	managedBin := ManagedCodexBin(home)
	managedIdentity, err := install.ExecutableIdentityFromFile(managedBin)
	if err != nil {
		t.Fatalf("ExecutableIdentityFromFile(next) error = %v", err)
	}
	if !managedIdentity.SameContents(runningIdentity) {
		t.Fatal("the two releases should ship identical executable bytes")
	}
	if managedIdentity == runningIdentity {
		t.Fatal("path-sensitive identities should differ for different releases")
	}
	// A version-only decision would report the daemon current; the identity
	// comparison must restart it instead.
	previousVersion := managedCodexVersionBestEffort
	managedCodexVersionBestEffort = func(string) *string {
		version := "1.0.0"
		return &version
	}
	t.Cleanup(func() { managedCodexVersionBestEffort = previousVersion })

	outcome, err := runner.TryRestartIfRunning(RestartIfBinaryOrVersionChanged, UpdaterRefreshNone, managedBin)
	if err != nil {
		t.Fatalf("TryRestartIfRunning error = %v", err)
	}
	if outcome != RestartRestarted {
		t.Fatalf("outcome = %s, want restarted", outcome)
	}
	if stub.appStops != 1 {
		t.Fatalf("app-server stops = %d, want 1", stub.appStops)
	}
}

// TestUpdateOnceScheduledNotReadyKeepsRetryingLikeRust pins the #50559 branch:
// a scheduled run with a stale updater and an unready daemon reports NotReady
// and keeps running instead of following the selection.
func TestUpdateOnceScheduledNotReadyKeepsRetryingLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	stub.appRunning = true
	stub.socketReady = false
	home := t.TempDir()
	publishStableSelection(t, home, "1.0.0")
	daemon := NewDaemonForCodexHome(home, "")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	managedIdentity, err := install.ExecutableIdentityFromFile(daemon.Paths.ManagedCodexBin)
	if err != nil {
		t.Fatalf("ExecutableIdentityFromFile error = %v", err)
	}
	startTime, err := readPIDProcessStartTime(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("readPIDProcessStartTime error = %v", err)
	}
	if err := WritePIDRecord(daemon.Paths.PIDFile, &PIDRecord{
		PID:                uint32(os.Getpid()),
		ProcessStartTime:   startTime,
		ExecutableIdentity: &managedIdentity,
	}); err != nil {
		t.Fatalf("WritePIDRecord(app-server) error = %v", err)
	}
	staleUpdater := install.ExecutableIdentityFromDataAndPath("stale-updater", []byte("stale"))
	options := &UpdateLoopOptions{
		Install:       func(context.Context, installerMode, string) error { return nil },
		ReadFile:      os.ReadFile,
		ReexecUpdater: func(string) error { return nil },
	}
	control, outcome, reexecBin, err := updateOnce(context.Background(), runner, &staleUpdater, options, UpdateTrigger{Kind: UpdateTriggerScheduled})
	if err != nil {
		t.Fatalf("updateOnce error = %v", err)
	}
	if control != UpdateLoopContinue {
		t.Fatalf("control = %s, want continue", control)
	}
	if outcome == nil || *outcome != RestartNotReady {
		t.Fatalf("outcome = %v, want not ready", outcome)
	}
	if strings.TrimSpace(reexecBin) != "" {
		t.Fatalf("reexecBin = %q, want none", reexecBin)
	}
}

// TestUpdateOnceScheduledNoOpPreservesDaemonLikeRust pins the other half of
// #50559: a stale updater adopts a daemon that is already current without
// replacing its process.
func TestUpdateOnceScheduledNoOpPreservesDaemonLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	stub.appRunning = true
	stub.socketReady = true
	stub.appVersion = "1.0.0"
	home := t.TempDir()
	publishStableSelection(t, home, "1.0.0")
	daemon := NewDaemonForCodexHome(home, "")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	managedIdentity, err := install.ExecutableIdentityFromFile(daemon.Paths.ManagedCodexBin)
	if err != nil {
		t.Fatalf("ExecutableIdentityFromFile error = %v", err)
	}
	startTime, err := readPIDProcessStartTime(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("readPIDProcessStartTime error = %v", err)
	}
	if err := WritePIDRecord(daemon.Paths.PIDFile, &PIDRecord{
		PID:                uint32(os.Getpid()),
		ProcessStartTime:   startTime,
		ExecutableIdentity: &managedIdentity,
	}); err != nil {
		t.Fatalf("WritePIDRecord(app-server) error = %v", err)
	}
	previousVersion := managedCodexVersionBestEffort
	managedCodexVersionBestEffort = func(string) *string {
		version := "1.0.0"
		return &version
	}
	t.Cleanup(func() { managedCodexVersionBestEffort = previousVersion })
	staleUpdater := install.ExecutableIdentityFromDataAndPath("stale-updater", []byte("stale"))
	options := &UpdateLoopOptions{
		Install:       func(context.Context, installerMode, string) error { return nil },
		ReadFile:      os.ReadFile,
		ReexecUpdater: func(string) error { return nil },
	}
	control, outcome, reexecBin, err := updateOnce(context.Background(), runner, &staleUpdater, options, UpdateTrigger{Kind: UpdateTriggerScheduled})
	if err != nil {
		t.Fatalf("updateOnce error = %v", err)
	}
	if control != UpdateLoopContinue {
		t.Fatalf("control = %s, want continue", control)
	}
	if outcome == nil || *outcome != RestartAlreadyCurrent {
		t.Fatalf("outcome = %v, want already current", outcome)
	}
	if strings.TrimSpace(reexecBin) != "" {
		t.Fatalf("reexecBin = %q, want none", reexecBin)
	}
	if stub.appStops != 0 {
		t.Fatalf("app-server stops = %d, want 0", stub.appStops)
	}
}
