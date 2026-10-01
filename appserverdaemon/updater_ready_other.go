//go:build !windows

package appserverdaemon

// The updater readiness handshake exists only for the Windows job/lock model
// (Rust cfg(windows) in backend/pid_windows.rs and update_loop.rs).

// WaitForUpdaterOwnership is a no-op off Windows.
func WaitForUpdaterOwnership(*PIDBackend) error { return nil }

// MarkUpdaterReady is a no-op off Windows.
func MarkUpdaterReady(*PIDBackend) error { return nil }

// finishUpdaterStart is a no-op off Windows.
func finishUpdaterStart(*PIDBackend, *PIDRecord) error { return nil }
