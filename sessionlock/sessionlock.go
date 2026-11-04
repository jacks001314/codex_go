// Package sessionlock observes the desktop session lock for local voice calls
// (Rust #59330 tui/session_lock). Setup takes an initial snapshot before audio
// starts; waiting blocks until the session locks, the detector fails, or the
// caller stops the observation.
package sessionlock

import (
	"errors"
	"time"
)

// DefaultPollInterval matches Rust's 250 ms lock polling.
const DefaultPollInterval = 250 * time.Millisecond

// ErrUnavailable reports a platform without a supported lock detector.
var ErrUnavailable = errors.New("session lock monitoring is unavailable on this platform")

// Detector reports whether the current desktop session is locked.
type Detector interface {
	Locked() (bool, error)
}

// Result is the outcome of one observation: the session locked, monitoring
// failed, or the observer was stopped (zero value with Stopped).
type Result struct {
	Locked  bool
	Stopped bool
	Err     error
}

// Wait observes the session until it locks, the detector fails, or stop is
// closed. It checks the initial state first, so an already locked session
// resolves immediately without any polling.
func Wait(detector Detector, interval time.Duration, stop <-chan struct{}) Result {
	if detector == nil {
		return Result{Err: ErrUnavailable}
	}
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	locked, err := detector.Locked()
	if err != nil {
		return Result{Err: err}
	}
	if locked {
		return Result{Locked: true}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return Result{Stopped: true}
		case <-ticker.C:
			locked, err := detector.Locked()
			if err != nil {
				return Result{Err: err}
			}
			if locked {
				return Result{Locked: true}
			}
		}
	}
}
