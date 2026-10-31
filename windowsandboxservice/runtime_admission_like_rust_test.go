//go:build windows

package windowsandboxservice

import (
	"errors"
	"testing"
	"time"

	"codex_go/sandbox/windowssandbox"
)

// useOwnerAdmissionSeams installs a permissive admission baseline; each test
// overrides only the seam it exercises.
func useOwnerAdmissionSeams(t *testing.T) {
	t.Helper()
	previousRemoved := admissionOwnerRemoved
	previousCleanUp := admissionCleanUp
	previousDisabled := admissionRestoreDisabled
	previousRestore := admissionRestoreOwner
	previousReport := admissionReportStopPending
	previousLoad := runtimeLoadInstallation
	previousSleep := cleanupRetrySleep
	t.Cleanup(func() {
		admissionOwnerRemoved = previousRemoved
		admissionCleanUp = previousCleanUp
		admissionRestoreDisabled = previousDisabled
		admissionRestoreOwner = previousRestore
		admissionReportStopPending = previousReport
		runtimeLoadInstallation = previousLoad
		cleanupRetrySleep = previousSleep
	})
	admissionRestoreDisabled = func() error { return nil }
	admissionRestoreOwner = func(*Lifecycle) {}
	admissionOwnerRemoved = func() (bool, error) { return false, nil }
	admissionCleanUp = func(*Lifecycle) error { return nil }
	admissionReportStopPending = func(*ServiceState) error { return nil }
	runtimeLoadInstallation = func() (*windowssandbox.InstallationRecord, error) { return nil, nil }
	cleanupRetrySleep = func(duration time.Duration) {}
}

func TestOwnerAdmissionKeepsAdmittingWhileTheOwnerIsInstalled(t *testing.T) {
	useOwnerAdmissionSeams(t)
	cleanups := 0
	admissionCleanUp = func(*Lifecycle) error {
		cleanups++
		return nil
	}
	state := &ServiceState{}
	admission := &ownerAdmission{lifecycle: &Lifecycle{}, lastCleanupError: "sandbox cleanup deferred: stale"}
	if err := admission.check(state); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	if cleanups != 0 || admission.cleaned || state.Shutdown() {
		t.Fatalf("check() cleaned %d times, cleaned %t, shutdown %t", cleanups, admission.cleaned, state.Shutdown())
	}
	if admission.lastCleanupError != "" {
		t.Fatalf("check() kept the stale cleanup error %q", admission.lastCleanupError)
	}
}

func TestOwnerAdmissionStopsAfterRetiringTheRemovedOwner(t *testing.T) {
	useOwnerAdmissionSeams(t)
	admissionOwnerRemoved = func() (bool, error) { return true, nil }
	cleanups := 0
	admissionCleanUp = func(lifecycle *Lifecycle) error {
		if lifecycle == nil {
			t.Error("cleanup ran without the lifecycle")
		}
		cleanups++
		return nil
	}
	reported := []uint32{}
	admissionReportStopPending = func(state *ServiceState) error {
		if !state.Shutdown() {
			t.Error("check() reported STOP_PENDING before stopping the listener")
		}
		reported = append(reported, ServiceStopPending)
		return nil
	}
	state := &ServiceState{}
	admission := &ownerAdmission{lifecycle: &Lifecycle{}}
	if err := admission.check(state); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	if cleanups != 1 {
		t.Fatalf("check() cleaned %d times, want 1", cleanups)
	}
	if !admission.cleaned || !state.Shutdown() {
		t.Fatalf("check() left cleaned %t and shutdown %t", admission.cleaned, state.Shutdown())
	}
	if len(reported) != 1 || reported[0] != ServiceStopPending {
		t.Fatalf("check() reported %v, want one STOP_PENDING", reported)
	}
}

func TestOwnerAdmissionReturnsTheCleanupErrorWithoutARuntimeRecord(t *testing.T) {
	useOwnerAdmissionSeams(t)
	admissionOwnerRemoved = func() (bool, error) { return true, nil }
	failure := errors.New("remove the sandbox accounts")
	admissionCleanUp = func(*Lifecycle) error { return failure }
	admission := &ownerAdmission{lifecycle: &Lifecycle{}}
	if err := admission.check(&ServiceState{}); !errors.Is(err, failure) {
		t.Fatalf("check() error = %v, want %v", err, failure)
	}
	if admission.cleaned {
		t.Fatal("check() reported a finished cleanup")
	}
}

func TestOwnerAdmissionReturnsTheCleanupErrorForARetiringRuntime(t *testing.T) {
	useOwnerAdmissionSeams(t)
	admissionOwnerRemoved = func() (bool, error) { return true, nil }
	failure := errors.New("interrupted runtime cleanup requires repair")
	admissionCleanUp = func(*Lifecycle) error { return failure }
	record := runtimeRecord(windowssandbox.RuntimeAccountRegistration{UserSID: "S-1-5-21-1-2-3-1001"})
	retiring := cleanupTestCurrentPackage
	record.Runtime.Retiring = &retiring
	runtimeLoadInstallation = func() (*windowssandbox.InstallationRecord, error) { return record, nil }
	if err := (&ownerAdmission{lifecycle: &Lifecycle{}}).check(&ServiceState{}); !errors.Is(err, failure) {
		t.Fatalf("check() error = %v, want %v", err, failure)
	}
}

func TestOwnerAdmissionRetriesUntilTheRuntimeRecordDisappears(t *testing.T) {
	useOwnerAdmissionSeams(t)
	admissionOwnerRemoved = func() (bool, error) { return true, nil }
	record := runtimeRecord(windowssandbox.RuntimeAccountRegistration{UserSID: "S-1-5-21-1-2-3-1001"})
	attempts := 0
	admissionCleanUp = func(*Lifecycle) error {
		attempts++
		if attempts == 1 {
			return errors.New("sandbox owner still shutting down")
		}
		return nil
	}
	runtimeLoadInstallation = func() (*windowssandbox.InstallationRecord, error) {
		if attempts == 1 {
			return record, nil
		}
		return nil, nil
	}
	admission := &ownerAdmission{lifecycle: &Lifecycle{}}
	if err := admission.check(&ServiceState{}); err != nil {
		t.Fatalf("check() error = %v", err)
	}
	if attempts != 2 || !admission.cleaned {
		t.Fatalf("check() attempts = %d with cleaned %t, want 2 and true", attempts, admission.cleaned)
	}
}

func TestOwnerAdmissionGivesUpWhenTheBackoffExpires(t *testing.T) {
	useOwnerAdmissionSeams(t)
	admissionOwnerRemoved = func() (bool, error) { return true, nil }
	failure := errors.New("sandbox owner still shutting down")
	admissionCleanUp = func(*Lifecycle) error { return failure }
	record := runtimeRecord(windowssandbox.RuntimeAccountRegistration{UserSID: "S-1-5-21-1-2-3-1001"})
	runtimeLoadInstallation = func() (*windowssandbox.InstallationRecord, error) { return record, nil }
	state := &ServiceState{}
	state.shutdown.Store(true)
	if err := (&ownerAdmission{lifecycle: &Lifecycle{}}).check(state); !errors.Is(err, failure) {
		t.Fatalf("check() error = %v, want %v", err, failure)
	}
}

func TestOwnerAdmissionFinishesAnUninstallingStop(t *testing.T) {
	useOwnerAdmissionSeams(t)
	removedCalls := 0
	admissionOwnerRemoved = func() (bool, error) {
		removedCalls++
		return false, nil
	}
	cleanups := 0
	admissionCleanUp = func(*Lifecycle) error {
		cleanups++
		return nil
	}
	lifecycle := &Lifecycle{}
	lifecycle.uninstalling.Store(true)
	state := &ServiceState{}
	state.stopRequested.Store(true)
	admission := &ownerAdmission{lifecycle: lifecycle}
	if err := admission.finish(state); err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if cleanups != 1 || removedCalls != 0 {
		t.Fatalf("finish() cleaned %d times with %d owner checks, want 1 and 0", cleanups, removedCalls)
	}
	if state.OwnerRemoved() {
		t.Fatal("finish() reported an owner removal without one")
	}
}

func TestOwnerAdmissionFinishesAStopForARemovedOwner(t *testing.T) {
	useOwnerAdmissionSeams(t)
	admissionOwnerRemoved = func() (bool, error) { return true, nil }
	cleanups := 0
	admissionCleanUp = func(*Lifecycle) error {
		cleanups++
		return nil
	}
	state := &ServiceState{}
	state.stopRequested.Store(true)
	if err := (&ownerAdmission{lifecycle: &Lifecycle{}}).finish(state); err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if cleanups != 1 {
		t.Fatalf("finish() cleaned %d times, want 1", cleanups)
	}
}

func TestOwnerAdmissionSkipsTheFinishCleanupWithoutAStop(t *testing.T) {
	useOwnerAdmissionSeams(t)
	removedCalls := 0
	admissionOwnerRemoved = func() (bool, error) {
		removedCalls++
		return true, nil
	}
	cleanups := 0
	admissionCleanUp = func(*Lifecycle) error {
		cleanups++
		return nil
	}
	if err := (&ownerAdmission{lifecycle: &Lifecycle{}}).finish(&ServiceState{}); err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if cleanups != 0 || removedCalls != 0 {
		t.Fatalf("finish() cleaned %d times with %d owner checks, want 0 and 0", cleanups, removedCalls)
	}
}

func TestOwnerAdmissionRecordsTheOwnerRemovedStopReason(t *testing.T) {
	useOwnerAdmissionSeams(t)
	admission := &ownerAdmission{lifecycle: &Lifecycle{}, cleaned: true}
	state := &ServiceState{}
	state.stopRequested.Store(true)
	if err := admission.finish(state); err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if !state.OwnerRemoved() {
		t.Fatal("finish() did not record the owner-removed stop reason")
	}
}

func TestOwnerAdmissionPropagatesRestoreAndOwnerCheckFailures(t *testing.T) {
	useOwnerAdmissionSeams(t)
	restoreFailure := errors.New("restore disabled sandbox accounts")
	admissionRestoreDisabled = func() error { return restoreFailure }
	if err := (&ownerAdmission{lifecycle: &Lifecycle{}}).check(&ServiceState{}); !errors.Is(err, restoreFailure) {
		t.Fatalf("check() error = %v, want %v", err, restoreFailure)
	}

	useOwnerAdmissionSeams(t)
	checkFailure := errors.New("read the recorded runtime")
	admissionOwnerRemoved = func() (bool, error) { return false, checkFailure }
	if err := (&ownerAdmission{lifecycle: &Lifecycle{}}).check(&ServiceState{}); !errors.Is(err, checkFailure) {
		t.Fatalf("check() error = %v, want %v", err, checkFailure)
	}
}

func TestLifecycleCleanUpRequiresALifecycle(t *testing.T) {
	var lifecycle *Lifecycle
	if err := lifecycle.CleanUp(); err == nil {
		t.Fatal("CleanUp() succeeded without a lifecycle")
	}
}

func TestRuntimeRetiringReportsTheRetirementFence(t *testing.T) {
	if runtimeRetiring(nil) {
		t.Fatal("runtimeRetiring() reported a missing record as retiring")
	}
	record := runtimeRecord(windowssandbox.RuntimeAccountRegistration{UserSID: "S-1-5-21-1-2-3-1001"})
	if runtimeRetiring(record) {
		t.Fatal("runtimeRetiring() reported a live runtime as retiring")
	}
	retiring := cleanupTestCurrentPackage
	record.Runtime.Retiring = &retiring
	if !runtimeRetiring(record) {
		t.Fatal("runtimeRetiring() ignored the retirement fence")
	}
}
