//go:build !windows

package windowssandbox

// PreparedSandboxCleanup is unavailable off Windows.
type PreparedSandboxCleanup struct{}

// PrepareSandboxCleanup reports that native sandbox cleanup is Windows-only.
func PrepareSandboxCleanup() (*PreparedSandboxCleanup, error) {
	return nil, unsupported("uninstall.prepare_packaged_windows_sandbox_cleanup")
}

// Finish reports that native sandbox cleanup is Windows-only.
func (prepared *PreparedSandboxCleanup) Finish(string, func(string), func() error) error {
	return unsupported("uninstall.finish_packaged_windows_sandbox_cleanup")
}

// Release does nothing off Windows.
func (prepared *PreparedSandboxCleanup) Release() {}
