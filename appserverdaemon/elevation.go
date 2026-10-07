package appserverdaemon

import "errors"

// ErrElevatedDaemonLauncher mirrors Rust
// backend::windows::ensure_not_elevated's refusal text.
var ErrElevatedDaemonLauncher = errors.New("start the Windows daemon from a non-elevated terminal; shared clients must not inherit administrator privileges")

// EnsureNonElevated refuses daemon lifecycle work from an elevated Windows
// process (Rust backend::windows::ensure_not_elevated): a shared app-server
// must not be launched by an administrator token whose clients would inherit
// those privileges. Off Windows the probe is always false, matching the
// `#[cfg(windows)]` gate on the Rust call sites.
func EnsureNonElevated() error {
	if currentProcessElevated() {
		return ErrElevatedDaemonLauncher
	}
	return nil
}

// IsElevated reports whether the launcher's own token is elevated, mirroring
// Rust app-server-daemon::is_elevated (#50803), which `remote-control` calls to
// decide daemon eligibility. Off Windows the probe is always false, matching the
// `#[cfg(windows)]` gate on the Rust call site, and the Go probe cannot fail
// where Rust's token query returns a Result.
func IsElevated() bool {
	return currentProcessElevated()
}
