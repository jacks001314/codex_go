//go:build windows

package windowsandboxservice

import (
	"errors"
	"sync/atomic"

	"codex_go/sandbox/windowssandbox"
)

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
	current, err := windowssandbox.InstallationIsCurrentPackage(record)
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
