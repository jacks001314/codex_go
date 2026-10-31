//go:build windows

package windowsandboxservice

import (
	"errors"
	"time"
)

const (
	// cleanupRetryInterval is the one-second teardown retry wait
	// (Rust runtime_lifecycle::retry_cleanup).
	cleanupRetryInterval = time.Second
	// cleanupRetryStopLimit lets four retries fit inside the 10 s SCM wait hint.
	cleanupRetryStopLimit = 5
	// cleanupRetryBackoffWaits bounds the pre-fence backoff loop.
	cleanupRetryBackoffWaits = 30
)

var cleanupRetrySleep = time.Sleep

// RetryCleanup ports runtime_lifecycle::retry_cleanup: it retries the current
// teardown step, never a phase recovered from stored intent. An SCM stop
// initiates uninstall cleanup rather than cancelling it, so a stop gets a
// bounded number of retries inside the service wait hint.
func RetryCleanup(operation func() error) error {
	if operation == nil {
		return errors.New("missing cleanup operation")
	}
	stopFailures := 0
	lastLogged := ""
	for {
		err := operation()
		if err == nil {
			return nil
		}
		state := serviceState.Load()
		if state == nil {
			return err
		}
		logCleanupError(err, &lastLogged)
		if state.StopRequested() {
			stopFailures++
			if stopFailures == cleanupRetryStopLimit {
				return err
			}
			cleanupRetrySleep(cleanupRetryInterval)
			continue
		}
		if !waitForCleanupRetry(state) && !state.StopRequested() {
			return err
		}
	}
}

// logCleanupError logs a deferred cleanup failure once per distinct message, so
// retrying an unchanged failure cannot flood the Windows event log.
func logCleanupError(err error, previous *string) {
	message := "sandbox cleanup deferred: " + ErrorMessage(err)
	if *previous != message {
		LogError(EventServiceFailed, message)
	}
	*previous = message
}

// waitForCleanupRetry keeps the pipe available during pre-fence backoff; a
// reinstall resumes admission on the next owner check and SCM stops are never
// reset.
func waitForCleanupRetry(state *ServiceState) bool {
	for wait := 0; wait < cleanupRetryBackoffWaits; wait++ {
		if state.Shutdown() {
			return false
		}
		cleanupRetrySleep(cleanupRetryInterval)
	}
	return !state.Shutdown()
}
