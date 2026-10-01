//go:build !windows

package appserverdaemon

// currentProcessElevated is always false off Windows, where the daemon
// lifecycle has no elevation handoff to refuse (Rust gates the check with
// `#[cfg(windows)]`).
var currentProcessElevated = func() bool { return false }
