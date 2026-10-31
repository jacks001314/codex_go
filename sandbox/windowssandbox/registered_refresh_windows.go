//go:build windows

package windowssandbox

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// RegisteredCoreNeedsRefresh reports a committed receipt for a different
// package version at the requested Codex home (Rust registered_core_needs_refresh).
// It is a startup hint only; the service rechecks ownership and readiness under
// its setup lock.
func RegisteredCoreNeedsRefresh(codexHome string) (bool, error) {
	fullName, hasPackage, err := PackageFullNameForProcess(windows.CurrentProcess())
	if err != nil {
		return false, fmt.Errorf("query the current package: %w", err)
	}
	if !hasPackage {
		return false, nil
	}
	record, err := LoadInstallation()
	if err != nil {
		return false, err
	}
	if record == nil {
		return false, nil
	}
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return false, err
	}
	canonicalHome, err := filepath.EvalSymlinks(codexHome)
	if err != nil {
		return false, fmt.Errorf("canonicalize Codex home: %w", err)
	}
	if record.CodexHome != canonicalHome || runtime.ReadyPackage == nil {
		return false, nil
	}
	ready := *runtime.ReadyPackage
	return ready != fullName && runtime.ReadyForPackage(ready), nil
}
