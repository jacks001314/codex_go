package windowssandbox

// CleanUpLegacyWindowsSandbox removes the machine-wide legacy Windows sandbox
// resources: the sandbox accounts and their group, the Winlogon hidden-user
// entries, the persistent sandbox WFP provider/sublayer/filters, and the
// offline firewall rules.
//
// It mirrors Rust windows-sandbox-rs
// `uninstall_windows::clean_up_legacy_windows_sandbox` (#50437): cleanup
// requires administrator privileges, runs without a Codex home, and therefore
// preserves every user file, including the sandbox directories and their
// filesystem permissions. `report` receives one human-readable line per step.
func CleanUpLegacyWindowsSandbox(report func(string)) error {
	return cleanUpLegacyWindowsSandbox(report)
}
