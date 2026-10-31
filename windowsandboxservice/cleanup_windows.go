//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

// directoryNotEmpty is ERROR_DIR_NOT_EMPTY, which Rust reports as
// io::ErrorKind::DirectoryNotEmpty.
const directoryNotEmpty syscall.Errno = 145

// SandboxCleanupFinisher is the prepared native cleanup phase the lifecycle
// drives between owner-scoped directory work (Rust
// PreparedWindowsSandboxCleanup::finish).
type SandboxCleanupFinisher interface {
	Finish(codexHome *string, report func(string), cleanUpDesktop func() error) error
}

// SandboxCleanupGuard is a prepared native cleanup that also owns the setup
// lock until Release (Rust PreparedWindowsSandboxCleanup plus Drop).
type SandboxCleanupGuard interface {
	SandboxCleanupFinisher
	Release()
}

var (
	_ SandboxCleanupFinisher = (*windowssandbox.PreparedSandboxCleanup)(nil)
	_ SandboxCleanupGuard    = (*windowssandbox.PreparedSandboxCleanup)(nil)
)

// The seams keep the destructive orchestration testable without sandbox
// accounts, elevated privileges or an authenticated owner token.
var (
	cleanupRemoveAll        = os.RemoveAll
	cleanupRemoveEmptyDir   = os.Remove
	cleanupPinAncestors     = pinExistingAncestors
	cleanupWithOwner        = withOwnerImpersonation
	cleanupRevokeSandboxACE = revokeCodexSandboxUsersACE
	cleanupIsDirectory      = func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.IsDir()
	}
)

// CleanUpResources derives cleanup.rs::clean_up: it removes the registered
// runtime metadata, finishes the prepared native cleanup, and removes only the
// desktop directories the uninstalling owner still owns.
func (lifecycle *Lifecycle) CleanUpResources(prepared SandboxCleanupFinisher, runtimeRecord *windowssandbox.InstallationRecord) error {
	if lifecycle == nil {
		return errors.New("missing sandbox lifecycle")
	}
	if prepared == nil {
		return errors.New("missing prepared sandbox cleanup")
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.installation == nil {
		return errors.New("the authenticated package installation was not recorded")
	}
	return cleanUpInstallation(lifecycle, lifecycle.installation, prepared, runtimeRecord)
}

// cleanUpInstallation keeps the owner's home and ancestors pinned until the
// desktop callback runs, and keeps native retries bound to the recorded owner
// by never unpinning the home before they finish (Rust cleanup::clean_up).
func cleanUpInstallation(
	lifecycle *Lifecycle,
	installation *lifecycleInstallation,
	prepared SandboxCleanupFinisher,
	runtimeRecord *windowssandbox.InstallationRecord,
) error {
	LogInformation(EventCleanupStarted, "sandbox uninstall cleanup started")
	codexHome := installation.codexHome
	// Remove exact grants from the locked cleanup record before native account
	// deletion.
	if runtimeRecord != nil {
		logCleanup("removing registered runtime metadata")
		if err := RemoveRuntimeMetadata(installation.token, runtimeRecord); err != nil {
			return fmt.Errorf("remove registered runtime metadata: %w", err)
		}
	}
	logCleanup("removing native sandbox resources")
	pruneCodexHome := false
	// Once owner-scoped deletion releases the home, retries must not traverse it
	// as LocalSystem.
	var sandboxHome *string
	if codexHome != "" && installation.guardPresent() {
		home := codexHome
		sandboxHome = &home
	}
	err := prepared.Finish(sandboxHome, logCleanup, func() error {
		if runtimeRecord != nil {
			allows, err := ownerAllowsCleanup(&lifecycle.uninstalling, runtimeRecord)
			if err != nil {
				return err
			}
			if !allows {
				// Only the old sandbox resources are repaired on reinstall. Never
				// remove the reinstalled app's desktop-created home or runtime cache.
				logCleanup("skipping desktop directories: owner reinstalled the app")
				return nil
			}
		}
		desktop := installation.record.DesktopInstallation
		if desktop == nil {
			logCleanup("skipping desktop directories: no desktop installation record")
			return nil
		}
		// The marker is user-writable. It must never authorize deletion as
		// LocalSystem.
		return cleanupWithOwner(installation.token, func() error {
			failures := []string{}
			recordResult := func(operation string, err error) {
				switch {
				case err == nil:
					logCleanup(operation + ": completed")
				case errors.Is(err, fs.ErrNotExist):
					logCleanup(operation + ": skipped, not found")
				default:
					message := fmt.Sprintf("%s: failed, %v", operation, err)
					LogError(EventCleanupDetail, message)
					failures = append(failures, message)
				}
			}
			if codexHome != "" {
				if desktop.CreatedCodexHome && windowssandbox.CleanUninstallEnabled(codexHome, lifecycle.preinstalled) {
					// Release the home itself so it can be deleted; keep its
					// ancestors pinned.
					installation.releaseGuard()
					recordResult("remove desktop-created codex home", cleanupRemoveAll(codexHome))
				} else {
					pruneCodexHome = true
					logCleanup("preserving codex home")
					// Retained data must not keep permissions for the deleted
					// sandbox group.
					recordResult("remove codex home sandbox permissions", cleanupRevokeSandboxACE(codexHome))
				}
			} else {
				logCleanup("skipping codex home: no pinned home")
			}
			// The cache may have been created after provisioning. Pin it only for
			// cleanup.
			if cleanupIsDirectory(desktop.CacheHome) {
				var cacheHandles []windows.Handle
				if err := cleanupPinAncestors(desktop.CacheHome, &cacheHandles); err != nil {
					LogError(
						EventCleanupDetail,
						fmt.Sprintf("skipping runtime cache: could not pin cache home, %v", err),
					)
					failures = append(failures, err.Error())
				} else {
					recordResult(
						"remove codex runtime cache",
						cleanupRemoveAll(filepath.Join(desktop.CacheHome, "codex-runtimes")),
					)
					// Release only the cache root; its ancestors stay pinned until
					// the desktop closure ends.
					if len(cacheHandles) > 0 {
						_ = windows.CloseHandle(cacheHandles[len(cacheHandles)-1])
						cacheHandles = cacheHandles[:len(cacheHandles)-1]
					}
					removeEmptyDirectory(desktop.CacheHome, "cache home")
					closeHandles(cacheHandles)
				}
			} else {
				logCleanup("skipping cache home: no accessible directory")
			}
			if len(failures) > 0 {
				return fmt.Errorf("remove desktop directories: %s", strings.Join(failures, "; "))
			}
			return nil
		})
	})
	if err != nil {
		return fmt.Errorf("remove packaged Windows sandbox resources: %w", err)
	}
	if pruneCodexHome && codexHome != "" {
		// Keep the home pinned through native retries. Empty-root pruning is best
		// effort so a failure cannot restart native cleanup through an unpinned
		// home.
		if err := cleanupWithOwner(installation.token, func() error {
			installation.releaseGuard()
			removeEmptyDirectory(codexHome, "codex home")
			return nil
		}); err != nil {
			LogError(EventCleanupDetail, fmt.Sprintf("remove empty codex home: failed, %v", err))
		}
	}
	if runtimeRecord != nil {
		LogInformation(EventCleanupDetail, "native sandbox cleanup finished; registered runtime cleanup pending")
	} else {
		LogInformation(EventCleanupFinished, "sandbox uninstall cleanup finished")
	}
	return nil
}

// removeEmptyDirectory prunes one empty root and reports every outcome
// (Rust cleanup::remove_empty_directory).
func removeEmptyDirectory(path string, target string) {
	err := cleanupRemoveEmptyDir(path)
	switch {
	case err == nil:
		logCleanup("removed empty " + target)
	case errors.Is(err, fs.ErrNotExist):
		logCleanup("skipping empty " + target + " removal: not found")
	case errors.Is(err, directoryNotEmpty):
		logCleanup("preserving " + target + ": directory is not empty")
	default:
		LogError(EventCleanupDetail, fmt.Sprintf("remove empty %s: failed, %v", target, err))
	}
}

// logCleanup records one cleanup outcome at detail level.
func logCleanup(message string) {
	LogInformation(EventCleanupDetail, message)
}

// revokeCodexSandboxUsersACE removes the deleted sandbox group's exact grants
// from a retained directory (Rust resolve_sid + revoke_ace).
func revokeCodexSandboxUsersACE(path string) error {
	sid, err := windowssandbox.ResolveAccountSID(runtimeSandboxUsersGroup)
	if err != nil {
		return err
	}
	return windowssandbox.RevokeACE(windowssandbox.ACLRequest{Path: path, SID: sid})
}
