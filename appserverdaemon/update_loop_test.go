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
	if identity == nil || *identity != identityOld || *identity == identityNew {
		t.Fatalf("identity = %#v, want %#v", identity, identityOld)
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
