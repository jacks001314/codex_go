//go:build darwin && !cgo

package sessionlock

// SystemDetector reports that macOS lock monitoring needs cgo; callers keep
// voice calls running because the platform cannot observe the lock.
func SystemDetector() Detector { return nil }
