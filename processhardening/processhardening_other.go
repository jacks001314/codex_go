//go:build !unix

package processhardening

// ApplyPreMain is a no-op on non-Unix platforms, matching Rust's Windows
// hardening TODO (pre_main_hardening_windows does nothing).
func ApplyPreMain() {}
