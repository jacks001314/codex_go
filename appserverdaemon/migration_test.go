package appserverdaemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/daemonrecovery"
)

// legacyReleasePath is the release directory of the legacy installation the
// migration test builds.
func legacyReleasePath(home string) string {
	return filepath.Join(home, "packages", legacyPackagesDirname, releasesDirName, "0.157.0-"+platformTarget())
}

// legacyStandaloneHome builds the pre-dedicated package layout the migration
// lane moves away from: `packages/standalone/current` plus the legacy state
// artifact that selects that package root.
func legacyStandaloneHome(t *testing.T) (home string, legacyRelease string) {
	t.Helper()
	home = t.TempDir()
	root := filepath.Join(home, "packages", legacyPackagesDirname)
	legacyRelease = legacyReleasePath(home)
	if err := os.MkdirAll(filepath.Join(legacyRelease, "bin"), 0o700); err != nil {
		t.Fatalf("MkdirAll legacy release error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyRelease, "bin", managedCodexFileName()), []byte("legacy"), 0o700); err != nil {
		t.Fatalf("WriteFile legacy entrypoint error = %v", err)
	}
	if err := selectDaemonRelease(root, legacyRelease); err != nil {
		t.Fatalf("selectDaemonRelease(legacy) error = %v", err)
	}
	// A legacy stderr log is what makes PackageRoot pick the standalone package.
	state := filepath.Join(home, StateDirName)
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatalf("MkdirAll state error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(state, legacyStderrLogName), nil, 0o600); err != nil {
		t.Fatalf("WriteFile legacy log error = %v", err)
	}
	return home, legacyRelease
}

// migrationInstaller stages the release the published installer would prepare,
// leaving the selection at the pending name for the cutover.
func migrationInstaller(t *testing.T, home string) func(context.Context, []byte, installerMode, string, string) error {
	return func(_ context.Context, _ []byte, mode installerMode, packageRoot, codexHome string) error {
		if mode.Kind != installerMigration {
			t.Fatalf("installer mode = %v, want migration", mode.Kind)
		}
		if codexHome != home {
			t.Fatalf("installer CODEX_HOME = %q, want %q", codexHome, home)
		}
		name := "1.0.0-" + platformTarget()
		release := filepath.Join(packageRoot, releasesDirName, name)
		if err := os.MkdirAll(filepath.Join(release, "bin"), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(release, "bin", managedCodexFileName()), []byte("1.0.0"), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(packageRoot, autoUpdateVersionFileName), []byte(name), 0o600); err != nil {
			return err
		}
		if err := selectDaemonRelease(packageRoot, release); err != nil {
			return err
		}
		return os.Rename(filepath.Join(packageRoot, "current"), filepath.Join(packageRoot, migrationPendingName))
	}
}

// migrationFakeEdges returns the injected edges the migration lane uses, with
// every lifecycle edge failing loudly because no daemon is running.
func migrationFakeEdges(t *testing.T, home string) MigrationOptions {
	return MigrationOptions{
		FetchScript:          func(context.Context) ([]byte, error) { return []byte("# " + installerDeferSelection + "\n"), nil },
		RunInstaller:         migrationInstaller(t, home),
		ManagedVersion:       func(string) (string, error) { return "1.0.0", nil },
		SupportsCommand:      func(string, []string) bool { return true },
		EnsureDetachedLaunch: func(string) error { return nil },
		StartBackend: func(*Daemon, *DaemonSettings) error {
			t.Fatal("no backend must start when none was running")
			return nil
		},
		WaitReady: func(*Daemon) (string, error) { t.Fatal("no readiness wait when none was running"); return "", nil },
		StopBackend: func(*Daemon, *DaemonSettings) error {
			t.Fatal("no backend must stop when none was running")
			return nil
		},
		StopUpdater:    func(*Daemon, *DaemonSettings) error { return nil },
		EnsureUpdater:  func(*Daemon, *DaemonSettings) error { return nil },
		DiscardPending: func(*Daemon) error { t.Fatal("no recovery to discard when none was running"); return nil },
		Diagnostic:     func(string, ...any) {},
	}
}

// TestRunDaemonMigrationLikeRust pins the legacy to dedicated migration the
// published installer drives (Rust update_loop_tests' migration case).
func TestRunDaemonMigrationLikeRust(t *testing.T) {
	// Each case starts from a clean legacy installation, so a pending selection
	// left by an earlier refused migration cannot mask the next one.
	newCase := func(t *testing.T) (home string, daemon *Daemon, dedicatedRoot string) {
		home, _ = legacyStandaloneHome(t)
		daemon = NewDaemonForCodexHome(home, "")
		daemon.refreshInstallation()
		if got := filepath.Base(daemon.Paths.PIDFile); got != LegacyPIDFileName {
			t.Fatalf("legacy pid file = %q, want %q", got, LegacyPIDFileName)
		}
		return home, daemon, filepath.Join(home, "packages", daemonPackagesDirname)
	}

	// A published installer that does not defer the selection is refused before
	// anything is prepared.
	home, daemon, dedicatedRoot := newCase(t)
	options := migrationFakeEdges(t, home)
	options.FetchScript = func(context.Context) ([]byte, error) { return []byte("#!/bin/sh\n"), nil }
	if _, err := runDaemonMigration(context.Background(), daemon, &options); err == nil ||
		!strings.Contains(err.Error(), "does not support daemon migration") {
		t.Fatalf("old installer error = %v", err)
	}
	if pathExistsNoFollow(filepath.Join(dedicatedRoot, "current")) {
		t.Fatal("the dedicated selection must not exist after a refused installer")
	}

	// A prepared release whose binary cannot migrate leaves the legacy daemon.
	home, daemon, dedicatedRoot = newCase(t)
	options = migrationFakeEdges(t, home)
	options.SupportsCommand = func(string, []string) bool { return false }
	if _, err := runDaemonMigration(context.Background(), daemon, &options); err == nil ||
		!strings.Contains(err.Error(), "does not support daemon migration yet") {
		t.Fatalf("incompatible release error = %v", err)
	}
	if pathExistsNoFollow(filepath.Join(dedicatedRoot, "current")) {
		t.Fatal("the dedicated selection must not exist after an incompatible release")
	}

	// A compatible release moves the daemon to its dedicated package.
	home, daemon, dedicatedRoot = newCase(t)
	options = migrationFakeEdges(t, home)
	output, err := runDaemonMigration(context.Background(), daemon, &options)
	if err != nil {
		t.Fatalf("runDaemonMigration error = %v", err)
	}
	if output == nil || output.Status != UpdateUpdated || output.Message != daemonMigrationMessage {
		t.Fatalf("output = %#v", output)
	}
	if output.InstalledVersion == nil || *output.InstalledVersion != "1.0.0" {
		t.Fatalf("installed version = %v", output.InstalledVersion)
	}
	if output.RunningVersion != nil {
		t.Fatalf("running version = %v, want none", output.RunningVersion)
	}
	if got := PackageRoot(home); got != dedicatedRoot {
		t.Fatalf("package root = %q, want %q", got, dedicatedRoot)
	}
	if want := filepath.Join(dedicatedRoot, "current", "bin", managedCodexFileName()); output.ManagedCodexPath != want {
		t.Fatalf("managed codex path = %q, want %q", output.ManagedCodexPath, want)
	}
	if pathExistsNoFollow(filepath.Join(dedicatedRoot, migrationPendingName)) {
		t.Fatal(".migration-current must be renamed away")
	}
	wantRelease := canonicalPath(filepath.Join(dedicatedRoot, releasesDirName, "1.0.0-"+platformTarget()))
	if got := canonicalPath(filepath.Join(dedicatedRoot, "current")); got != wantRelease {
		t.Fatalf("dedicated current = %q, want %q", got, wantRelease)
	}
	// The legacy CLI package is left unchanged.
	wantLegacy := canonicalPath(legacyReleasePath(home))
	if got := canonicalPath(filepath.Join(home, "packages", legacyPackagesDirname, "current")); got != wantLegacy {
		t.Fatalf("legacy current = %q, want %q", got, wantLegacy)
	}
}

// TestDiscardPendingRecoveryLikeRust pins that a stale recovery snapshot is
// removed before a planned replacement and a missing one is not an error.
func TestDiscardPendingRecoveryLikeRust(t *testing.T) {
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "")
	path := daemonrecovery.FilePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	if err := discardPendingRecovery(daemon); err != nil {
		t.Fatalf("discardPendingRecovery error = %v", err)
	}
	if pathExistsNoFollow(path) {
		t.Fatal("the recovery snapshot must be removed")
	}
	if err := discardPendingRecovery(daemon); err != nil {
		t.Fatalf("discardPendingRecovery(missing) error = %v", err)
	}
}

// TestRequestManualUpdateMigratesLegacySelectionLikeRust pins that a CLI request
// against a legacy standalone daemon migrates it instead of forwarding a
// request to its updater (Rust update_loop::request_manual_update).
func TestRequestManualUpdateMigratesLegacySelectionLikeRust(t *testing.T) {
	home, _ := legacyStandaloneHome(t)
	edges := migrationFakeEdges(t, home)
	output, err := RequestManualUpdate(context.Background(), home, &ManualUpdateOptions{Migration: &edges})
	if err != nil {
		t.Fatalf("RequestManualUpdate error = %v", err)
	}
	if output == nil || output.Status != UpdateUpdated || output.Message != daemonMigrationMessage {
		t.Fatalf("output = %#v", output)
	}
	if got := PackageRoot(home); got != filepath.Join(home, "packages", daemonPackagesDirname) {
		t.Fatalf("package root = %q, want the dedicated package", got)
	}
}
