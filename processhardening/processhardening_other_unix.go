//go:build unix && !linux && !android && !darwin && !freebsd && !openbsd

package processhardening

// ApplyPreMain is a no-op on other Unix platforms (Rust only hardens
// Linux/macOS/BSD specifically).
func ApplyPreMain() {}
