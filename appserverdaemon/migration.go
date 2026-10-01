package appserverdaemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"codex_go/daemonrecovery"
)

// Legacy to dedicated daemon migration (Rust app-server-daemon/src/migration.rs).
//
// A daemon that still serves the pre-dedicated `packages/standalone` package is
// moved to its own `packages/app-server-daemon` package by one explicit update.
// The installer prepares a latest-channel release without publishing a
// selection, and the cutover renames `.migration-current` to `current` only
// after the prepared binary proves it supports the migration command. The
// legacy CLI package is never modified.

// daemonMigrationMessage is the success message (Rust migration::run).
const daemonMigrationMessage = "The daemon was updated and moved to its dedicated package. The legacy CLI package was left unchanged."

// migrationPendingName is the staging selection the installer publishes before
// the cutover (Rust `.migration-current`).
const migrationPendingName = ".migration-current"

// discardPendingRecovery removes a stale daemon recovery snapshot before a
// planned replacement (Rust thread_recovery::discard_pending). The file itself
// is the shared managed-restart candidate set (daemonrecovery).
func discardPendingRecovery(daemon *Daemon) error {
	home := daemonCodexHome(daemon)
	if home == "" {
		return ErrDaemonPathsRequired
	}
	err := os.Remove(daemonrecovery.FilePath(home))
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("failed to clear daemon recovery file: %w", err)
}

// migrationEntrypoint is the app-server executable inside a migrated release
// (Rust migration::run's entrypoint).
func migrationEntrypoint() string {
	if runtime.GOOS == "windows" {
		return "bin/codex.exe"
	}
	return "bin/codex"
}

// MigrationOptions injects the installer, probing, and lifecycle edges the
// migration lane uses so a test can run it without spawning installers or
// app-servers.
type MigrationOptions struct {
	FetchScript          func(context.Context) ([]byte, error)
	RunInstaller         func(context.Context, []byte, installerMode, string, string) error
	ManagedVersion       func(string) (string, error)
	SupportsCommand      func(string, []string) bool
	EnsureDetachedLaunch func(string) error
	// StartBackend starts the migrated app-server (Rust start_managed_backend).
	StartBackend func(*Daemon, *DaemonSettings) error
	// WaitReady waits for the migrated app-server to answer (Rust wait_until_ready).
	WaitReady func(*Daemon) (string, error)
	// StopBackend stops the legacy app-server with the configured grace.
	StopBackend func(*Daemon, *DaemonSettings) error
	// StopUpdater stops the legacy managed updater.
	StopUpdater func(*Daemon, *DaemonSettings) error
	// EnsureUpdater makes the daemon's managed updater match its package.
	EnsureUpdater func(*Daemon, *DaemonSettings) error
	// DiscardPending clears a daemon's pending recovery snapshot.
	DiscardPending func(*Daemon) error
	// Diagnostic records a progress or warning line.
	Diagnostic func(format string, args ...any)
}

func normalizeMigrationOptions(options *MigrationOptions) *MigrationOptions {
	if options == nil {
		options = &MigrationOptions{}
	}
	if options.FetchScript == nil {
		options.FetchScript = fetchInstallerScript
	}
	if options.RunInstaller == nil {
		options.RunInstaller = runInstallerScript
	}
	if options.ManagedVersion == nil {
		options.ManagedVersion = managedCodexVersion
	}
	if options.SupportsCommand == nil {
		options.SupportsCommand = supportsDaemonCommand
	}
	if options.EnsureDetachedLaunch == nil {
		options.EnsureDetachedLaunch = ensureDetachedLaunch
	}
	if options.StartBackend == nil {
		options.StartBackend = func(daemon *Daemon, settings *DaemonSettings) error {
			_, err := startPIDBackend(NewPIDBackend(daemon.BackendPaths(settings)))
			return err
		}
	}
	if options.WaitReady == nil {
		options.WaitReady = func(daemon *Daemon) (string, error) {
			return NewLifecycleRunner(daemon).waitUntilReady()
		}
	}
	if options.StopBackend == nil {
		options.StopBackend = func(daemon *Daemon, settings *DaemonSettings) error {
			return stopPIDBackend(NewPIDBackend(daemon.BackendPaths(settings)), settings.ShutdownGraceSecondsValue())
		}
	}
	if options.StopUpdater == nil {
		options.StopUpdater = stopDaemonUpdater
	}
	if options.EnsureUpdater == nil {
		options.EnsureUpdater = func(daemon *Daemon, settings *DaemonSettings) error {
			_, err := ensureManagedUpdater(daemon, settings)
			return err
		}
	}
	if options.DiscardPending == nil {
		options.DiscardPending = discardPendingRecovery
	}
	if options.Diagnostic == nil {
		options.Diagnostic = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}
	return options
}

// runDaemonMigration moves a legacy standalone daemon to the dedicated package
// (Rust migration::run). The caller must not already hold the operation lock.
func runDaemonMigration(ctx context.Context, daemon *Daemon, options *MigrationOptions) (*UpdateOutput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	options = normalizeMigrationOptions(options)
	operationLock, err := NewLifecycleRunner(daemon).acquireOperationLock()
	if err != nil {
		return nil, err
	}
	defer operationLock.Close()

	root, previousRelease, _, err := selectedRelease(daemon)
	if err != nil {
		return nil, err
	}
	if filepath.Base(root) != legacyPackagesDirname {
		return nil, errors.New("daemon selection changed; retry the update")
	}
	supported, err := manualUpdateSupported(daemon)
	if err != nil {
		return nil, err
	}
	if !supported {
		return nil, errors.New("daemon selection changed; retry the update")
	}
	home := packageRootCodexHome(root)
	dedicatedRoot := filepath.Join(home, "packages", daemonPackagesDirname)
	settings, err := daemon.LoadSettings()
	if err != nil {
		return nil, err
	}
	running, err := prepareRunningBackend(daemon, settings)
	if err != nil {
		return nil, err
	}
	if !running && prepareSocketAnswers(daemon) {
		return unsupportedUpdateOutput(daemon), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	script, err := options.FetchScript(ctx)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(script, []byte(installerDeferSelection)) {
		return nil, errors.New("the published installer does not support daemon migration yet; the legacy installation was left unchanged")
	}
	options.Diagnostic("Preparing the daemon update in %s...", dedicatedRoot)
	if err := options.RunInstaller(ctx, script, installerMode{Kind: installerMigration}, dedicatedRoot, home); err != nil {
		return nil, err
	}
	installLock, err := acquireExclusiveFileLock(filepath.Join(dedicatedRoot, installLockFileName), installLockTimeout, installLockRetry, "daemon installer lock")
	if err != nil {
		return nil, err
	}
	defer installLock.Close()

	pending := filepath.Join(dedicatedRoot, migrationPendingName)
	release := canonicalPath(pending)
	if release == "" {
		return nil, errors.New("installer did not prepare a daemon package")
	}
	name := filepath.Base(release)
	releases := canonicalPath(filepath.Join(dedicatedRoot, releasesDirName))
	if releases == "" || filepath.Dir(release) != releases {
		return nil, errors.New("installer did not prepare a latest-channel daemon package")
	}
	marker, err := os.ReadFile(filepath.Join(dedicatedRoot, autoUpdateVersionFileName))
	if err != nil || strings.TrimSpace(string(marker)) != name {
		return nil, errors.New("installer did not prepare a latest-channel daemon package")
	}
	binary := filepath.Join(release, filepath.FromSlash(migrationEntrypoint()))
	version, err := options.ManagedVersion(binary)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(name, version+"-") {
		return nil, errors.New("prepared daemon version does not match its release")
	}
	// An older production updater understands only the legacy package/PID paths.
	if !options.SupportsCommand(binary, []string{"pid-update-loop", "--check-package-ownership"}) {
		return nil, errors.New("the latest production release does not support daemon migration yet; the legacy daemon was left running")
	}
	if err := options.EnsureDetachedLaunch(binary); err != nil {
		return nil, err
	}
	if currentRelease, err := selectedReleaseName(daemon); err != nil || currentRelease != previousRelease {
		return nil, errors.New("daemon selection changed; retry the update")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cutoverErr := func() error {
		if err := options.StopUpdater(daemon, settings); err != nil {
			return err
		}
		if currentRelease, err := selectedReleaseName(daemon); err != nil || currentRelease != previousRelease {
			return errors.New("daemon selection changed; retry the update")
		}
		if running {
			if err := options.DiscardPending(daemon); err != nil {
				return err
			}
			if err := options.StopBackend(daemon, settings); err != nil {
				return err
			}
		}
		if err := os.Rename(pending, filepath.Join(dedicatedRoot, "current")); err != nil {
			return fmt.Errorf("failed to select the new daemon package; retry the update: %w", err)
		}
		return nil
	}()
	if cutoverErr != nil {
		if restoreErr := options.EnsureUpdater(daemon, settings); restoreErr != nil {
			options.Diagnostic("warning: failed to restore the legacy updater after migration failed: %v", restoreErr)
		}
		return nil, cutoverErr
	}

	selected := NewDaemonForCodexHome(home, daemon.CLIVersion)
	selected.refreshInstallation()
	var runningVersion *string
	if running {
		if err := options.StartBackend(selected, settings); err != nil {
			return nil, fmt.Errorf("daemon migrated but could not start; retry with `codex app-server daemon start`: %w", err)
		}
		ready, err := options.WaitReady(selected)
		if err != nil {
			return nil, fmt.Errorf("daemon migrated but is not ready; retry with `codex app-server daemon start`: %w", err)
		}
		runningVersion = &ready
	}
	if err := options.EnsureUpdater(selected, settings); err != nil {
		options.Diagnostic("warning: daemon migrated but its updater could not start: %v", err)
	}
	return &UpdateOutput{
		Status:           UpdateUpdated,
		ManagedCodexPath: selected.Paths.ManagedCodexBin,
		InstalledVersion: &version,
		RunningVersion:   runningVersion,
		Message:          daemonMigrationMessage,
	}, nil
}

// selectedReleaseName reports the resolved release path of the daemon's current
// selection, which the cutover re-checks before and after it stops the updater.
func selectedReleaseName(daemon *Daemon) (string, error) {
	_, release, _, err := selectedRelease(daemon)
	return release, err
}
