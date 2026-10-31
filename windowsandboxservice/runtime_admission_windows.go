//go:build windows

package windowsandboxservice

import (
	"errors"

	"codex_go/sandbox/windowssandbox"
)

// The seams keep owner-aware admission testable without a live installation
// store, managed accounts or an SCM status handle.
var (
	admissionOwnerRemoved      = RuntimeOwnerRemoved
	admissionCleanUp           = (*Lifecycle).CleanUp
	admissionRestoreDisabled   = RestoreDisabledRuntimeAccounts
	admissionRestoreOwner      = restoreOwnerForBroker
	admissionReportStopPending = func(state *ServiceState) error {
		return state.ReportStatus(ServiceStopPending, NoError)
	}
)

// ownerAdmission ports runtime_lifecycle::run's admission closure: the recorded
// owner is restored before every admission, and a sandbox whose owner no longer
// has the app installed is retired before another client is accepted.
// Retirement fences are never replayed from stored intent, so each attempt acts
// only on the live invocation.
type ownerAdmission struct {
	lifecycle        *Lifecycle
	cleaned          bool
	lastCleanupError string
}

// restoreOwner restores durable temporary-enable intent and the recorded owner
// before admission (Rust runtime_lifecycle::run restore_owner). A missing
// watcher does not stop the listener: other clients can still use elevated
// setup, so only the account restoration failure is fatal.
func (admission *ownerAdmission) restoreOwner() error {
	if err := admissionRestoreDisabled(); err != nil {
		return err
	}
	admissionRestoreOwner(admission.lifecycle)
	return nil
}

// check runs between admissions and returns nil while the owner is still
// installed. Once the owner's package is gone it retires the sandbox, stops the
// service and reports STOP_PENDING, or retries teardown during the pre-fence
// backoff so a reinstall can resume admission.
func (admission *ownerAdmission) check(state *ServiceState) error {
	if state == nil {
		return errors.New("the sandbox service state is missing")
	}
	for {
		if err := admission.restoreOwner(); err != nil {
			return err
		}
		removed, err := admissionOwnerRemoved()
		if err != nil {
			return err
		}
		if !removed {
			admission.lastCleanupError = ""
			return nil
		}
		cleanupErr := admissionCleanUp(admission.lifecycle)
		if cleanupErr == nil {
			admission.cleaned = true
			state.shutdown.Store(true)
			return admissionReportStopPending(state)
		}
		record, err := runtimeLoadInstallation()
		if err != nil {
			return err
		}
		if record == nil || runtimeRetiring(record) {
			return cleanupErr
		}
		logCleanupError(cleanupErr, &admission.lastCleanupError)
		// Keep the pipe available during pre-fence backoff. A reinstall resumes
		// admission on the next owner check; SCM stops are never reset.
		if !waitForCleanupRetry(state) {
			return cleanupErr
		}
	}
}

// finish performs the owner-aware teardown an SCM stop can trigger after the
// listener closed (Rust runtime_lifecycle::run's post-broker check) and records
// why the service is stopping.
func (admission *ownerAdmission) finish(state *ServiceState) error {
	if state == nil {
		return errors.New("the sandbox service state is missing")
	}
	if !admission.cleaned && state.StopRequested() {
		uninstalling := admission.lifecycle != nil && admission.lifecycle.Uninstalling() != nil &&
			admission.lifecycle.Uninstalling().Load()
		removed := false
		if !uninstalling {
			var err error
			removed, err = admissionOwnerRemoved()
			if err != nil {
				return err
			}
		}
		if uninstalling || removed {
			if err := admissionCleanUp(admission.lifecycle); err != nil {
				return err
			}
		}
	}
	if admission.cleaned {
		state.ownerRemoved.Store(true)
	}
	return nil
}

// runtimeRetiring reports whether the recorded runtime is already inside a
// retirement, which needs repair instead of another cleanup attempt.
func runtimeRetiring(record *windowssandbox.InstallationRecord) bool {
	if record == nil {
		return false
	}
	runtime, err := record.RuntimeRegistration()
	if err != nil {
		return false
	}
	return runtime.Retiring != nil
}
