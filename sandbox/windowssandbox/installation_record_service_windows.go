//go:build windows

package windowssandbox

import (
	"errors"
	"strings"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox/provisionservice"
)

var currentInstallationPackageFamily = func() (string, bool, error) {
	return provisionservice.ProcessPackageFamily(windows.CurrentProcess())
}

// SaveInstallationRecord ports Rust installation_record::save: it serializes with
// the machine-wide setup lock, never persists a caller-supplied runtime, refuses
// a record owned by a different registered sandbox and preserves fresh
// registered-Core state instead of overwriting it with an owner-only snapshot.
func SaveInstallationRecord(record *InstallationRecord) (*InstallationRecord, error) {
	if record == nil {
		return nil, errors.New("missing installation record")
	}
	lock, err := AcquireSandboxSetupLock(5000)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	record.Runtime = nil
	current, err := LoadRuntimeInstallation()
	if err != nil {
		return nil, err
	}
	if current != nil {
		family, present, err := currentInstallationPackageFamily()
		if err != nil {
			return nil, err
		}
		if !present {
			return nil, errors.New("the provisioning service has no package identity")
		}
		if err := current.AdmitOwner(record, family); err != nil {
			return nil, err
		}
		record.Runtime = current.Runtime
	}
	if err := SaveInstallation(record); err != nil {
		return nil, err
	}
	return record, nil
}

// InstallationIsCurrentPackage reports whether the recorded runtime belongs to
// the running package family (Rust is_current_package_family).
func InstallationIsCurrentPackage(record *InstallationRecord) (bool, error) {
	if record == nil || record.Runtime == nil {
		return false, errors.New("the installation record has no registered runtime")
	}
	family, present, err := currentInstallationPackageFamily()
	if err != nil {
		return false, err
	}
	if !present {
		return false, nil
	}
	return strings.EqualFold(record.Runtime.PackageFamily, family), nil
}
