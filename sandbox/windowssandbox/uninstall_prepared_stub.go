//go:build !windows

package windowssandbox

// RetainedLogonToken is unavailable off Windows: there are no logon tokens to
// retain, so the stub stands in for the Windows token type.
type RetainedLogonToken struct{}

// PreparedSandboxCleanup is unavailable off Windows.
type PreparedSandboxCleanup struct{}

// PrepareSandboxCleanup reports that native sandbox cleanup is Windows-only.
func PrepareSandboxCleanup() (*PreparedSandboxCleanup, error) {
	return nil, unsupported("uninstall.prepare_packaged_windows_sandbox_cleanup")
}

// PrepareSandboxCleanupWithRetainedTokens reports that native sandbox cleanup
// is Windows-only.
func PrepareSandboxCleanupWithRetainedTokens([]RetainedLogonToken) (*PreparedSandboxCleanup, error) {
	return nil, unsupported("uninstall.prepare_packaged_windows_sandbox_cleanup_with_retained_tokens")
}

// Finish reports that native sandbox cleanup is Windows-only.
func (prepared *PreparedSandboxCleanup) Finish(string, func(string), func() error) error {
	return unsupported("uninstall.finish_packaged_windows_sandbox_cleanup")
}

// Release does nothing off Windows.
func (prepared *PreparedSandboxCleanup) Release() {}
