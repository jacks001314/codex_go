//go:build windows

package windowsandboxservice

import (
	"errors"
	"sync/atomic"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

// runtimeRemoval is the prepared managed-account removal the cleanup
// transaction commits or aborts (Rust registered_runtime::removal::PreparedRemoval).
type runtimeRemoval interface {
	Tokens() []windows.Token
	Commit() error
	Abort()
}

// The seams keep the registered cleanup transaction testable without a
// packaged service, live managed accounts or the embedded PowerShell child.
var (
	prepareSandboxCleanupForUninstall = func(tokens []windows.Token) (SandboxCleanupGuard, error) {
		return windowssandbox.PrepareSandboxCleanupWithRetainedTokens(tokens)
	}
	prepareRuntimeRemoval = func(token windows.Token, record *windowssandbox.InstallationRecord) (runtimeRemoval, error) {
		return PrepareRuntimeRemoval(token, record)
	}
	runCleanupWithRetry                 = RetryCleanup
	cleanUpSandboxResources             = (*Lifecycle).CleanUpResources
	runtimeInstallationIsCurrentPackage = windowssandbox.InstallationIsCurrentPackage
)

// CleanUpRegisteredRuntime ports package_lifecycle::registered::clean_up: it
// refuses cleanup for another package family, an unfinished retirement, or an
// owner that still has the app installed, prepares exact managed logons and
// native cleanup under the setup lock, saves the retirement fence, and commits
// the removal only after the guard succeeded.
func (lifecycle *Lifecycle) CleanUpRegisteredRuntime(record *windowssandbox.InstallationRecord) error {
	if lifecycle == nil {
		return errors.New("missing sandbox lifecycle")
	}
	if record == nil {
		return errors.New("missing installation record")
	}
	current, err := runtimeInstallationIsCurrentPackage(record)
	if err != nil {
		return err
	}
	if !current {
		return nil
	}
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return err
	}
	if runtime.Retiring != nil {
		return errors.New("interrupted runtime cleanup requires repair")
	}
	allows, err := ownerAllowsCleanup(&lifecycle.uninstalling, record)
	if err != nil {
		return err
	}
	if !allows {
		return errors.New("registered runtime owner still has the app installed")
	}
	token, err := lifecycle.authenticatedOwnerToken(record)
	if err != nil {
		return err
	}
	record, err = PrepareRuntimeCleanup(record)
	if err != nil {
		return err
	}
	removal, err := prepareRuntimeRemoval(token, record)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			removal.Abort()
		}
	}()
	prepared, err := prepareSandboxCleanupForUninstall(removal.Tokens())
	if err != nil {
		return err
	}
	defer prepared.Release()
	// A reinstall before this boundary cancels without retiring resources.
	allows, err = ownerAllowsCleanup(&lifecycle.uninstalling, record)
	if err != nil {
		return err
	}
	if !allows {
		return errors.New("owner reinstalled before native cleanup started")
	}
	if err := runtimeSaveInstallation(record); err != nil {
		return err
	}
	// Retry only in this live invocation, retaining the outer setup lock. A
	// stored retirement fence is never proof that native cleanup finished.
	if err := runCleanupWithRetry(func() error {
		return cleanUpSandboxResources(lifecycle, prepared, record)
	}); err != nil {
		return err
	}
	if err := removal.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// authenticatedOwnerToken returns the retained owner token only when the
// recorded installation still matches the runtime record's owner and home.
func (lifecycle *Lifecycle) authenticatedOwnerToken(record *windowssandbox.InstallationRecord) (windows.Token, error) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	installation := lifecycle.installation
	if installation == nil {
		return 0, errors.New("authenticated installation is missing")
	}
	if installation.record.UserSID != record.UserSID || installation.codexHome != record.CodexHome {
		return 0, errors.New("waiting for the authenticated runtime owner and home before sandbox file cleanup")
	}
	return installation.token, nil
}

// ownerAllowsCleanup reports whether the owner still allows deleting desktop
// data. A reinstalled package, a retaining managed registration, or a successor
// version all keep their desktop files (Rust registered::owner_allows_cleanup).
func ownerAllowsCleanup(uninstalling *atomic.Bool, record *windowssandbox.InstallationRecord) (bool, error) {
	if record == nil {
		return false, errors.New("missing installation record")
	}
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return false, err
	}
	packages, err := runtimeRegisteredPackages(record.UserSID, runtime.PackageFamily)
	if err != nil {
		return false, err
	}
	if len(packages) == 0 {
		return true, nil
	}
	if uninstalling == nil || !uninstalling.Load() {
		return false, nil
	}
	retaining, err := RuntimeHasRetainingRegistration(record)
	if err != nil {
		return false, err
	}
	if retaining {
		return false, nil
	}
	// SCM may stop us before this exact package is removed. A successor version
	// belongs to an update or reinstall and must keep its desktop data.
	retiring, hasRetiring, err := runtimeProcessPackageFullName()
	if err != nil {
		return false, err
	}
	if !hasRetiring {
		return false, errors.New("the sandbox service has no package identity")
	}
	for _, name := range packages {
		if name != retiring {
			return false, nil
		}
	}
	return true, nil
}

// RuntimeOwnerRemoved reports whether the recorded managed runtime belongs to
// this package family and its owner no longer has the app installed
// (Rust registered::runtime_owner_removed).
func RuntimeOwnerRemoved() (bool, error) {
	record, err := runtimeLoadInstallation()
	if err != nil || record == nil {
		return false, err
	}
	current, err := runtimeInstallationIsCurrentPackage(record)
	if err != nil || !current {
		return false, err
	}
	hasPackage, err := runtimeOwnerHasPackage(record)
	if err != nil {
		return false, err
	}
	return !hasPackage, nil
}

// runtimeOwnerHasPackage reports whether the owner still has a package from the
// recorded managed family registered (Rust registered::runtime_owner_has_package).
func runtimeOwnerHasPackage(record *windowssandbox.InstallationRecord) (bool, error) {
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return false, err
	}
	packages, err := runtimeRegisteredPackages(record.UserSID, runtime.PackageFamily)
	if err != nil {
		return false, err
	}
	return len(packages) != 0, nil
}
