//go:build !windows

package appserverdaemon

// ensureDetachedLaunch is a no-op off Windows: the preflight exists only for the
// Windows Job Object model (Rust cfg(windows)).
func ensureDetachedLaunch(string) error { return nil }
