//go:build !windows

package windowssandbox

import "golang.org/x/sys/windows"

// PreparedSandboxCleanup is unavailable off Windows.
type PreparedSandboxCleanup struct{}

// PrepareSandboxCleanup reports that native sandbox cleanup is Windows-only.
func PrepareSandboxCleanup() (*PreparedSandboxCleanup, error) {
	return nil, unsupported("uninstall.prepare_packaged_windows_sandbox_cleanup")
}

// PrepareSandboxCleanupWithRetainedTokens reports that native sandbox cleanup
// is Windows-only.
func PrepareSandboxCleanupWithRetainedTokens([]windows.Token) (*PreparedSandboxCleanup, error) {
	return nil, unsupported("uninstall.prepare_packaged_windows_sandbox_cleanup_with_retained_tokens")
}

// Finish reports that native sandbox cleanup is Windows-only.
func (prepared *PreparedSandboxCleanup) Finish(string, func(string), func() error) error {
	return unsupported("uninstall.finish_packaged_windows_sandbox_cleanup")
}

// Release does nothing off Windows.
func (prepared *PreparedSandboxCleanup) Release() {}
