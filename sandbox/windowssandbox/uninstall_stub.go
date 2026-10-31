//go:build !windows

package windowssandbox

func cleanUpLegacyWindowsSandbox(report func(string)) error {
	return unsupported("uninstall.clean_up_legacy_windows_sandbox")
}

func cleanUpPackagedWindowsSandbox(codexHome string, report func(string), cleanUpDesktop func() error) error {
	return unsupported("uninstall.clean_up_packaged_windows_sandbox")
}

// ResolveAccountSID reports that accounts cannot be resolved off Windows.
func ResolveAccountSID(name string) (string, error) {
	return "", unsupported("uninstall.resolve_sid")
}
