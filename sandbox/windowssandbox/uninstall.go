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

// CleanUpPackagedWindowsSandbox removes the sandbox resources created for one
// authenticated installation. It serializes with setup, disables the sandbox
// accounts, stops their processes, removes the pinned sandbox directories,
// runs cleanUpDesktop while the accounts stay disabled, and then removes the
// network rules, hidden-user entries, accounts, and group (Rust #42375).
func CleanUpPackagedWindowsSandbox(codexHome string, report func(string), cleanUpDesktop func() error) error {
	return cleanUpPackagedWindowsSandbox(codexHome, report, cleanUpDesktop)
}
