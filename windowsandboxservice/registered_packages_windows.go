//go:build windows

package windowsandboxservice

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

const (
	// packageManagerClassID is the activatable runtime class the Rust service
	// uses for per-user package queries and registrations.
	packageManagerClassID = "Windows.Management.Deployment.PackageManager"
	// packageManagerFindByUserAndFamily is
	// IPackageManager.FindPackagesByUserSecurityIdPackageFamilyName.
	packageManagerFindByUserAndFamily = iInspectableMethodCount + 14
)

// packageManagerIID is IID_IPackageManager.
var packageManagerIID = windows.GUID{
	Data1: 0x9a7d4b65,
	Data2: 0x5e8f,
	Data3: 0x4fc7,
	Data4: [8]byte{0xa2, 0xe5, 0x7f, 0x69, 0x25, 0xcb, 0x8b, 0x53},
}

// PackageManager is an activated Windows.Management.Deployment.PackageManager.
type PackageManager struct {
	instance uintptr
}

// OpenPackageManager activates the deployment package manager for the calling
// user, mirroring Rust PackageManager::new.
func OpenPackageManager() (*PackageManager, error) {
	instance, err := activateRuntimeClass(packageManagerClassID, &packageManagerIID)
	if err != nil {
		return nil, fmt.Errorf("open the deployment package manager: %w", err)
	}
	return &PackageManager{instance: instance}, nil
}

// Close releases the deployment package manager.
func (manager *PackageManager) Close() {
	if manager == nil || manager.instance == 0 {
		return
	}
	comRelease(manager.instance)
	manager.instance = 0
}

// FindPackagesByUserSecurityIDPackageFamilyName returns the full names of every
// package registered for the given user and family (Rust
// registered_packages). Absence must be authoritative, so iteration errors are
// reported instead of truncated.
func (manager *PackageManager) FindPackagesByUserSecurityIDPackageFamilyName(userSID string, family string) ([]string, error) {
	if manager == nil || manager.instance == 0 {
		return nil, fmt.Errorf("deployment package manager is not open")
	}
	user, err := newRuntimeString(userSID)
	if err != nil {
		return nil, err
	}
	defer deleteRuntimeString(user)
	familyName, err := newRuntimeString(family)
	if err != nil {
		return nil, err
	}
	defer deleteRuntimeString(familyName)
	var iterable uintptr
	if err := comCall(
		manager.instance,
		packageManagerFindByUserAndFamily,
		user,
		familyName,
		uintptr(unsafe.Pointer(&iterable)),
	); err != nil {
		return nil, err
	}
	if iterable == 0 {
		return nil, nil
	}
	defer comRelease(iterable)
	return packageFullNames(iterable)
}

// packageFullNames walks an IIterable<Package> and collects full names.
func packageFullNames(iterable uintptr) ([]string, error) {
	var iterator uintptr
	if err := comCall(iterable, iIterableFirst, uintptr(unsafe.Pointer(&iterator))); err != nil {
		return nil, err
	}
	if iterator == 0 {
		return nil, nil
	}
	defer comRelease(iterator)
	names := make([]string, 0, 4)
	// WinRT booleans occupy one byte; a zeroed word reads them portably.
	var hasCurrent uintptr
	for {
		if err := comCall(iterator, iIteratorHasCurrent, uintptr(unsafe.Pointer(&hasCurrent))); err != nil {
			return nil, err
		}
		if hasCurrent == 0 {
			return names, nil
		}
		var current uintptr
		if err := comCall(iterator, iIteratorCurrent, uintptr(unsafe.Pointer(&current))); err != nil {
			return nil, err
		}
		name, err := packageFullName(current)
		comRelease(current)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
		if err := comCall(iterator, iIteratorMoveNext, uintptr(unsafe.Pointer(&hasCurrent))); err != nil {
			return nil, err
		}
	}
}

// packageFullName reads IPackage.Id().FullName().
func packageFullName(pkg uintptr) (string, error) {
	if pkg == 0 {
		return "", fmt.Errorf("package enumeration returned no package")
	}
	var id uintptr
	if err := comCall(pkg, iPackageID, uintptr(unsafe.Pointer(&id))); err != nil {
		return "", err
	}
	if id == 0 {
		return "", fmt.Errorf("package enumeration returned no package identity")
	}
	defer comRelease(id)
	var fullName uintptr
	if err := comCall(id, iPackageIdFullName, uintptr(unsafe.Pointer(&fullName))); err != nil {
		return "", err
	}
	defer deleteRuntimeString(fullName)
	return runtimeStringValue(fullName), nil
}

// RegisteredRuntimePackages returns the full names of the packages installed
// for userSID in family (Rust registered_runtime::registered_packages).
func RegisteredRuntimePackages(userSID string, family string) ([]string, error) {
	var names []string
	err := withRuntimeApartment(func() error {
		manager, err := OpenPackageManager()
		if err != nil {
			return err
		}
		defer manager.Close()
		names, err = manager.FindPackagesByUserSecurityIDPackageFamilyName(userSID, family)
		return err
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

// runtimeRegisteredPackages is the seam retention checks use so unit tests can
// stand in for the Windows package repository.
var runtimeRegisteredPackages = RegisteredRuntimePackages

// RuntimeHasRetainingRegistration reports whether any recorded account still
// has a package from this record's family. An intent is not an anchor: a failed
// registration may leave no managed package behind.
func RuntimeHasRetainingRegistration(record *windowssandbox.InstallationRecord) (bool, error) {
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return false, err
	}
	for _, account := range runtime.Accounts {
		packages, err := runtimeRegisteredPackages(account.UserSID, runtime.PackageFamily)
		if err != nil {
			return false, err
		}
		if len(packages) != 0 {
			return true, nil
		}
	}
	return false, nil
}
