//go:build windows

package windowsandboxservice

import (
	"sync/atomic"

	"codex_go/sandbox/windowssandbox"
)

// packageUninstallEvent is the part of the uninstall notification the retention
// policy reads (Rust registered::on_package_uninstalling).
type packageUninstallEvent interface {
	IsComplete() (bool, error)
}

// The seams keep the uninstall policy testable without a packaged service, the
// installation store or the provisioning pipe.
var (
	packageUninstallOwnerRecord = func() (*windowssandbox.InstallationRecord, error) {
		return runtimeLoadInstallation()
	}
	packageUninstallKeepsRegistration = RuntimeHasRetainingRegistration
	packageUninstallWakeListener      = WakeListener
	packageUninstallLogError          = func(message string) { LogError(EventServiceFailed, message) }
)

// onPackageUninstalling ports package_lifecycle::registered::on_package_uninstalling:
// an old version removed during an update must not retire the managed
// registration, so retention is decided by the whole family; only an owner with
// no managed package left marks the service as uninstalling.
func onPackageUninstalling(event packageUninstallEvent, ownerSID string, uninstalling *atomic.Bool) error {
	if event == nil {
		return nil
	}
	retention, err := packageUninstallRetention(ownerSID)
	if err != nil {
		packageUninstallLogError("unable to read registered runtime ownership: " + ErrorMessage(err))
		return nil
	}
	switch {
	case retention == nil:
		// The recorded runtime belongs to another owner; nothing to decide here.
		return nil
	case *retention:
		// Check the whole family only after the owner's uninstall finished.
		complete, err := event.IsComplete()
		if err != nil {
			return err
		}
		if complete {
			packageUninstallWakeListener()
		}
		return nil
	default:
		// No actual managed registration keeps this service installed, even if a
		// failed attempt left intent. Retain SCM-stop cleanup.
		complete, err := event.IsComplete()
		if err != nil {
			return err
		}
		if uninstalling != nil {
			uninstalling.Store(!complete)
		}
		return nil
	}
}

// packageUninstallRetention reports whether a managed registration still keeps
// this service installed. A nil result means the event belongs to another owner
// (Rust installation_record::load_runtime).
func packageUninstallRetention(ownerSID string) (*bool, error) {
	record, err := packageUninstallOwnerRecord()
	if err != nil {
		return nil, err
	}
	if record == nil {
		keep := false
		return &keep, nil
	}
	if record.UserSID != ownerSID {
		return nil, nil
	}
	keep, err := packageUninstallKeepsRegistration(record)
	if err != nil {
		return nil, err
	}
	return &keep, nil
}
