//go:build windows

package windowsandboxservice

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

// registeredCleanupOwnerSID is the owner runtimeRecord records.
const registeredCleanupOwnerSID = "S-1-5-21-1-2-3-1000"

// fakeRemoval records whether the transaction committed or aborted.
type fakeRemoval struct {
	tokens    []windows.Token
	committed bool
	aborted   bool
}

func (removal *fakeRemoval) Tokens() []windows.Token { return removal.tokens }

func (removal *fakeRemoval) Commit() error {
	removal.committed = true
	return nil
}

func (removal *fakeRemoval) Abort() { removal.aborted = true }

// fakeCleanupGuard records that the setup-lock guard was released.
type fakeCleanupGuard struct{ released bool }

func (guard *fakeCleanupGuard) Finish(*string, func(string), func() error) error { return nil }

func (guard *fakeCleanupGuard) Release() { guard.released = true }

// useRegisteredCleanupSeams installs a permissive packaged-cleanup baseline;
// each test overrides only the seam it exercises.
func useRegisteredCleanupSeams(t *testing.T) {
	t.Helper()
	previousPackages := runtimeRegisteredPackages
	previousFullName := runtimeProcessPackageFullName
	previousSave := runtimeSaveInstallation
	previousResolve := runtimeResolveSIDString
	previousFlags := runtimeLocalUserFlags
	previousPrepareSandbox := prepareSandboxCleanupForUninstall
	previousPrepareRemoval := prepareRuntimeRemoval
	previousRetry := runCleanupWithRetry
	previousCleanup := cleanUpSandboxResources
	previousCurrent := runtimeInstallationIsCurrentPackage
	t.Cleanup(func() {
		runtimeRegisteredPackages = previousPackages
		runtimeProcessPackageFullName = previousFullName
		runtimeSaveInstallation = previousSave
		runtimeResolveSIDString = previousResolve
		runtimeLocalUserFlags = previousFlags
		prepareSandboxCleanupForUninstall = previousPrepareSandbox
		prepareRuntimeRemoval = previousPrepareRemoval
		runCleanupWithRetry = previousRetry
		cleanUpSandboxResources = previousCleanup
		runtimeInstallationIsCurrentPackage = previousCurrent
	})
	runtimeInstallationIsCurrentPackage = func(*windowssandbox.InstallationRecord) (bool, error) { return true, nil }
	runtimeRegisteredPackages = func(userSID string, _ string) ([]string, error) {
		if userSID == registeredCleanupOwnerSID {
			return []string{cleanupTestCurrentPackage}, nil
		}
		return nil, nil
	}
	runtimeProcessPackageFullName = func() (string, bool, error) {
		return cleanupTestCurrentPackage, true, nil
	}
	runtimeSaveInstallation = func(*windowssandbox.InstallationRecord) error { return nil }
	runtimeLocalUserFlags = func(string) (uint32, bool, error) { return 0, false, nil }
	runtimeResolveSIDString = func(string) (string, error) {
		return "", errors.New("unexpected SID resolution")
	}
	prepareRuntimeRemoval = func(windows.Token, *windowssandbox.InstallationRecord) (runtimeRemoval, error) {
		return &fakeRemoval{}, nil
	}
	prepareSandboxCleanupForUninstall = func([]windows.Token) (SandboxCleanupGuard, error) {
		return &fakeCleanupGuard{}, nil
	}
	runCleanupWithRetry = func(operation func() error) error { return operation() }
	cleanUpSandboxResources = func(*Lifecycle, SandboxCleanupFinisher, *windowssandbox.InstallationRecord) error {
		return nil
	}
}

// registeredCleanupLifecycle returns a lifecycle whose retained owner matches
// the runtime record and whose uninstall flag is set.
func registeredCleanupLifecycle(t *testing.T, record *windowssandbox.InstallationRecord) *Lifecycle {
	t.Helper()
	lifecycle := &Lifecycle{
		installation: &lifecycleInstallation{
			record:    record,
			codexHome: record.CodexHome,
			token:     7,
		},
	}
	lifecycle.uninstalling.Store(true)
	return lifecycle
}

func TestCleanUpRegisteredRuntimePreparesRetiresAndCommitsLikeRust(t *testing.T) {
	useRegisteredCleanupSeams(t)
	record := runtimeRecord(windowssandbox.RuntimeAccountRegistration{
		Account: windowssandbox.SandboxRuntimeAccountOffline,
		UserSID: "S-1-5-21-1-2-3-1001",
	})
	events := []string{}
	retiring := cleanupTestCurrentPackage
	removal := &fakeRemoval{tokens: []windows.Token{11, 12}}
	guard := &fakeCleanupGuard{}
	prepareRuntimeRemoval = func(token windows.Token, prepared *windowssandbox.InstallationRecord) (runtimeRemoval, error) {
		events = append(events, "prepare-removal")
		if token != 7 {
			t.Errorf("owner token = %d, want 7", token)
		}
		prepared.Runtime.Retiring = &retiring
		return removal, nil
	}
	prepareSandboxCleanupForUninstall = func(tokens []windows.Token) (SandboxCleanupGuard, error) {
		events = append(events, "prepare-sandbox")
		if len(tokens) != 2 || tokens[0] != 11 {
			t.Errorf("retained tokens = %v, want the removal tokens", tokens)
		}
		return guard, nil
	}
	runtimeSaveInstallation = func(*windowssandbox.InstallationRecord) error {
		events = append(events, "save")
		return nil
	}
	cleanUpSandboxResources = func(_ *Lifecycle, _ SandboxCleanupFinisher, cleaned *windowssandbox.InstallationRecord) error {
		events = append(events, "cleanup")
		if cleaned.Runtime.Retiring == nil {
			t.Error("cleanup ran before the retirement fence was saved")
		}
		return nil
	}
	lifecycle := registeredCleanupLifecycle(t, record)
	if err := lifecycle.CleanUpRegisteredRuntime(record); err != nil {
		t.Fatalf("CleanUpRegisteredRuntime() error = %v", err)
	}
	want := []string{"prepare-removal", "prepare-sandbox", "save", "cleanup"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for index := range want {
		if events[index] != want[index] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
	if !removal.committed || removal.aborted {
		t.Fatalf("removal committed=%v aborted=%v", removal.committed, removal.aborted)
	}
	if !guard.released {
		t.Fatal("prepared native cleanup guard was not released")
	}
}

func TestCleanUpRegisteredRuntimeRefusesAnotherOwnedPackageLikeRust(t *testing.T) {
	useRegisteredCleanupSeams(t)
	record := runtimeRecord(windowssandbox.RuntimeAccountRegistration{
		Account: windowssandbox.SandboxRuntimeAccountOffline,
		UserSID: "S-1-5-21-1-2-3-1001",
	})
	prepared := false
	prepareRuntimeRemoval = func(windows.Token, *windowssandbox.InstallationRecord) (runtimeRemoval, error) {
		prepared = true
		return &fakeRemoval{}, nil
	}
	lifecycle := registeredCleanupLifecycle(t, record)
	lifecycle.uninstalling.Store(false)
	err := lifecycle.CleanUpRegisteredRuntime(record)
	if err == nil || err.Error() != "registered runtime owner still has the app installed" {
		t.Fatalf("CleanUpRegisteredRuntime() error = %v", err)
	}
	if prepared {
		t.Fatal("removal was prepared for an owner that still has the app installed")
	}
}

func TestCleanUpRegisteredRuntimeCancelsWhenTheOwnerReinstallsLikeRust(t *testing.T) {
	useRegisteredCleanupSeams(t)
	record := runtimeRecord(windowssandbox.RuntimeAccountRegistration{
		Account: windowssandbox.SandboxRuntimeAccountOffline,
		UserSID: "S-1-5-21-1-2-3-1001",
	})
	queries := 0
	runtimeRegisteredPackages = func(userSID string, _ string) ([]string, error) {
		if userSID != record.UserSID {
			return nil, nil
		}
		queries++
		if queries > 1 {
			return []string{cleanupTestSuccessorPackage}, nil
		}
		return []string{cleanupTestCurrentPackage}, nil
	}
	removal := &fakeRemoval{}
	guard := &fakeCleanupGuard{}
	prepareRuntimeRemoval = func(windows.Token, *windowssandbox.InstallationRecord) (runtimeRemoval, error) {
		return removal, nil
	}
	prepareSandboxCleanupForUninstall = func([]windows.Token) (SandboxCleanupGuard, error) {
		return guard, nil
	}
	saved := false
	runtimeSaveInstallation = func(*windowssandbox.InstallationRecord) error {
		saved = true
		return nil
	}
	cleaned := false
	cleanUpSandboxResources = func(*Lifecycle, SandboxCleanupFinisher, *windowssandbox.InstallationRecord) error {
		cleaned = true
		return nil
	}
	lifecycle := registeredCleanupLifecycle(t, record)
	err := lifecycle.CleanUpRegisteredRuntime(record)
	if err == nil || err.Error() != "owner reinstalled before native cleanup started" {
		t.Fatalf("CleanUpRegisteredRuntime() error = %v", err)
	}
	if !removal.aborted || removal.committed {
		t.Fatalf("removal committed=%v aborted=%v", removal.committed, removal.aborted)
	}
	if !guard.released {
		t.Fatal("prepared native cleanup guard was not released")
	}
	if saved || cleaned {
		t.Fatalf("saved=%v cleaned=%v, want neither", saved, cleaned)
	}
}

func TestCleanUpRegisteredRuntimeRetriesBeforeCommittingLikeRust(t *testing.T) {
	useRegisteredCleanupSeams(t)
	installCleanupRetryState(t, &ServiceState{})
	stubCleanupRetrySleep(t)
	record := runtimeRecord(windowssandbox.RuntimeAccountRegistration{
		Account: windowssandbox.SandboxRuntimeAccountOffline,
		UserSID: "S-1-5-21-1-2-3-1001",
	})
	removal := &fakeRemoval{}
	prepareRuntimeRemoval = func(windows.Token, *windowssandbox.InstallationRecord) (runtimeRemoval, error) {
		return removal, nil
	}
	prepareSandboxCleanupForUninstall = func([]windows.Token) (SandboxCleanupGuard, error) {
		return &fakeCleanupGuard{}, nil
	}
	runCleanupWithRetry = RetryCleanup
	attempts := 0
	cleanUpSandboxResources = func(*Lifecycle, SandboxCleanupFinisher, *windowssandbox.InstallationRecord) error {
		attempts++
		if attempts < 3 {
			return errors.New("native cleanup is still running")
		}
		return nil
	}
	lifecycle := registeredCleanupLifecycle(t, record)
	if err := lifecycle.CleanUpRegisteredRuntime(record); err != nil {
		t.Fatalf("CleanUpRegisteredRuntime() error = %v", err)
	}
	if attempts != 3 || !removal.committed {
		t.Fatalf("attempts = %d, committed = %v", attempts, removal.committed)
	}
}

func TestCleanUpRegisteredRuntimeSkipsAnotherPackageFamilyLikeRust(t *testing.T) {
	useRegisteredCleanupSeams(t)
	runtimeInstallationIsCurrentPackage = func(*windowssandbox.InstallationRecord) (bool, error) {
		return false, nil
	}
	record := runtimeRecord(windowssandbox.RuntimeAccountRegistration{
		Account: windowssandbox.SandboxRuntimeAccountOffline,
		UserSID: "S-1-5-21-1-2-3-1001",
	})
	lifecycle := registeredCleanupLifecycle(t, record)
	if err := lifecycle.CleanUpRegisteredRuntime(record); err != nil {
		t.Fatalf("CleanUpRegisteredRuntime() error = %v", err)
	}
}

func TestCleanUpRegisteredRuntimeRequiresTheRecordedOwnerHomeLikeRust(t *testing.T) {
	useRegisteredCleanupSeams(t)
	record := runtimeRecord(windowssandbox.RuntimeAccountRegistration{
		Account: windowssandbox.SandboxRuntimeAccountOffline,
		UserSID: "S-1-5-21-1-2-3-1001",
	})
	lifecycle := registeredCleanupLifecycle(t, record)
	lifecycle.installation.codexHome = ""
	err := lifecycle.CleanUpRegisteredRuntime(record)
	if err == nil || err.Error() != "waiting for the authenticated runtime owner and home before sandbox file cleanup" {
		t.Fatalf("CleanUpRegisteredRuntime() error = %v", err)
	}
}
