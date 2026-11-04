//go:build !windows && !darwin && !linux

package sessionlock

// SystemDetector reports that this platform has no lock detector; callers keep
// voice calls running because the platform cannot observe the lock.
func SystemDetector() Detector { return nil }
