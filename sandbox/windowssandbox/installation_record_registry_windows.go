//go:build windows

package windowssandbox

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var procRegFlushKey = windows.NewLazySystemDLL("advapi32.dll").NewProc("RegFlushKey")

// readInstallationRecordValue reads the bounded REG_SZ record. A missing key or
// value is reported as absent rather than as an error.
func readInstallationRecordValue(key string) ([]byte, bool, error) {
	handle, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read protected sandbox installation record: %w", err)
	}
	defer handle.Close()
	length, valueType, err := handle.GetValue(InstallationValue, nil)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read protected sandbox installation record: %w", err)
	}
	if valueType != registry.SZ {
		return nil, false, fmt.Errorf("read protected sandbox installation record: unexpected value type %d", valueType)
	}
	if length <= 0 || length > maxInstallationValueUnits*2 {
		return nil, false, errors.New("sandbox installation record has an invalid length")
	}
	data := make([]byte, length)
	read, _, err := handle.GetValue(InstallationValue, data)
	if err != nil {
		return nil, false, fmt.Errorf("read protected sandbox installation record: %w", err)
	}
	return data[:read], true, nil
}

// writeInstallationRecordValue persists the record into an existing protected
// key. The installer owns key creation, matching Rust's RegSetKeyValueW.
func writeInstallationRecordValue(key string, text string) error {
	handle, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("persist protected sandbox installation record: %w", err)
	}
	defer handle.Close()
	if err := handle.SetStringValue(InstallationValue, text); err != nil {
		return fmt.Errorf("persist protected sandbox installation record: %w", err)
	}
	return nil
}

// deleteInstallationRecordValue removes only the record value; key pruning is
// best effort at the installer/service boundary.
func deleteInstallationRecordValue(key string) error {
	handle, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("remove protected legacy sandbox installation record: %w", err)
	}
	defer handle.Close()
	if err := handle.DeleteValue(InstallationValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("remove protected legacy sandbox installation record: %w", err)
	}
	return nil
}

// flushInstallationRecordKey persists the record before an operation may
// outlive this process.
func flushInstallationRecordKey(key string) error {
	handle, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("open runtime ownership record: %w", err)
	}
	defer handle.Close()
	status, _, callErr := procRegFlushKey.Call(uintptr(handle))
	if status != 0 {
		return fmt.Errorf("flush runtime ownership record: %w", callErr)
	}
	return nil
}
