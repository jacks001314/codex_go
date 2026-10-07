//go:build !windows

package windowssandbox

func cleanUpLegacyWindowsSandbox(report func(string)) error {
	return unsupported("uninstall.clean_up_legacy_windows_sandbox")
}
