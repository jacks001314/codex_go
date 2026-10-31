//go:build windows

package windowsandboxservice

import (
	"errors"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

// noUninstallSubscription stands in for the owner's package catalog in tests
// that do not exercise the uninstall notification stream.
func noUninstallSubscription(string, string, *atomic.Bool, windows.Token) (*packageUninstallSubscription, error) {
	return nil, nil
}

// stubPackageUninstallSeams replaces the ownership and wake seams for one test.
func stubPackageUninstallSeams(
	t *testing.T,
	load func() (*windowssandbox.InstallationRecord, error),
	retaining func(*windowssandbox.InstallationRecord) (bool, error),
	logged *[]string,
	wakes *int,
) {
	t.Helper()
	previousLoad := packageUninstallOwnerRecord
	previousRetaining := packageUninstallKeepsRegistration
	previousWake := packageUninstallWakeListener
	previousLog := packageUninstallLogError
	t.Cleanup(func() {
		packageUninstallOwnerRecord = previousLoad
		packageUninstallKeepsRegistration = previousRetaining
		packageUninstallWakeListener = previousWake
		packageUninstallLogError = previousLog
	})
	packageUninstallOwnerRecord = load
	packageUninstallKeepsRegistration = retaining
	packageUninstallLogError = func(message string) { *logged = append(*logged, message) }
	packageUninstallWakeListener = func() { *wakes++ }
}

type packageUninstallTestEvent struct {
	complete    bool
	completeErr error
	reads       int
}

func (event *packageUninstallTestEvent) IsComplete() (bool, error) {
	event.reads++
	return event.complete, event.completeErr
}

func TestOnPackageUninstallingMarksAnUnwatchedOwnerFromTheEvent(t *testing.T) {
	var logged []string
	wakes := 0
	stubPackageUninstallSeams(t, func() (*windowssandbox.InstallationRecord, error) {
		return nil, nil
	}, func(*windowssandbox.InstallationRecord) (bool, error) {
		t.Fatal("retention was read without a recorded runtime")
		return false, nil
	}, &logged, &wakes)

	var uninstalling atomic.Bool
	event := &packageUninstallTestEvent{complete: false}
	if err := onPackageUninstalling(event, "S-1-5-21-1-2-3-1000", &uninstalling); err != nil {
		t.Fatalf("onPackageUninstalling() error = %v", err)
	}
	if !uninstalling.Load() {
		t.Fatal("onPackageUninstalling() ignored a started uninstall")
	}
	event.complete = true
	if err := onPackageUninstalling(event, "S-1-5-21-1-2-3-1000", &uninstalling); err != nil {
		t.Fatalf("onPackageUninstalling() error = %v", err)
	}
	if uninstalling.Load() {
		t.Fatal("onPackageUninstalling() kept the uninstalling flag after the owner's uninstall completed")
	}
	if wakes != 0 {
		t.Fatalf("onPackageUninstalling() woke the listener %d times without a retaining registration", wakes)
	}
	if len(logged) != 0 {
		t.Fatalf("onPackageUninstalling() logged %v", logged)
	}
}

func TestOnPackageUninstallingIgnoresAnotherOwnersRuntime(t *testing.T) {
	var logged []string
	wakes := 0
	stubPackageUninstallSeams(t, func() (*windowssandbox.InstallationRecord, error) {
		return &windowssandbox.InstallationRecord{UserSID: "S-1-5-21-9-9-9"}, nil
	}, func(*windowssandbox.InstallationRecord) (bool, error) {
		t.Fatal("retention was read for another owner")
		return false, nil
	}, &logged, &wakes)

	var uninstalling atomic.Bool
	event := &packageUninstallTestEvent{complete: true}
	if err := onPackageUninstalling(event, "S-1-5-21-1-2-3-1000", &uninstalling); err != nil {
		t.Fatalf("onPackageUninstalling() error = %v", err)
	}
	if event.reads != 0 {
		t.Fatalf("onPackageUninstalling() read the event %d times, want 0", event.reads)
	}
	if uninstalling.Load() {
		t.Fatal("onPackageUninstalling() changed the flag for another owner's runtime")
	}
	if wakes != 0 || len(logged) != 0 {
		t.Fatalf("onPackageUninstalling() woke %d times and logged %v", wakes, logged)
	}
}

func TestOnPackageUninstallingWakesOnlyAfterARetainedUninstallCompletes(t *testing.T) {
	var logged []string
	wakes := 0
	stubPackageUninstallSeams(t, func() (*windowssandbox.InstallationRecord, error) {
		return &windowssandbox.InstallationRecord{UserSID: "S-1-5-21-1-2-3-1000"}, nil
	}, func(*windowssandbox.InstallationRecord) (bool, error) {
		return true, nil
	}, &logged, &wakes)

	var uninstalling atomic.Bool
	event := &packageUninstallTestEvent{complete: false}
	if err := onPackageUninstalling(event, "S-1-5-21-1-2-3-1000", &uninstalling); err != nil {
		t.Fatalf("onPackageUninstalling() error = %v", err)
	}
	if wakes != 0 {
		t.Fatalf("onPackageUninstalling() woke the listener before the update finished")
	}
	if uninstalling.Load() {
		t.Fatal("onPackageUninstalling() retired a registration another package still retains")
	}
	event.complete = true
	if err := onPackageUninstalling(event, "S-1-5-21-1-2-3-1000", &uninstalling); err != nil {
		t.Fatalf("onPackageUninstalling() error = %v", err)
	}
	if wakes != 1 {
		t.Fatalf("onPackageUninstalling() woke the listener %d times, want 1", wakes)
	}
}

func TestOnPackageUninstallingLogsAnUnreadableOwnership(t *testing.T) {
	var logged []string
	wakes := 0
	stubPackageUninstallSeams(t, func() (*windowssandbox.InstallationRecord, error) {
		return nil, errors.New("read SOFTWARE\\OpenAI\\Codex")
	}, func(*windowssandbox.InstallationRecord) (bool, error) {
		t.Fatal("retention was read without a recorded runtime")
		return false, nil
	}, &logged, &wakes)

	var uninstalling atomic.Bool
	event := &packageUninstallTestEvent{complete: false}
	if err := onPackageUninstalling(event, "S-1-5-21-1-2-3-1000", &uninstalling); err != nil {
		t.Fatalf("onPackageUninstalling() error = %v", err)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "unable to read registered runtime ownership") {
		t.Fatalf("onPackageUninstalling() logged %v", logged)
	}
	if uninstalling.Load() || event.reads != 0 {
		t.Fatalf("onPackageUninstalling() acted on unreadable ownership: flag %t, reads %d", uninstalling.Load(), event.reads)
	}
}

func TestOnPackageUninstallingPropagatesEventFailures(t *testing.T) {
	var logged []string
	wakes := 0
	stubPackageUninstallSeams(t, func() (*windowssandbox.InstallationRecord, error) {
		return nil, nil
	}, func(*windowssandbox.InstallationRecord) (bool, error) {
		return true, nil
	}, &logged, &wakes)

	failure := errors.New("event is closed")
	var uninstalling atomic.Bool
	if err := onPackageUninstalling(&packageUninstallTestEvent{completeErr: failure}, "S-1-5-21-1-2-3-1000", &uninstalling); !errors.Is(err, failure) {
		t.Fatalf("onPackageUninstalling() error = %v, want %v", err, failure)
	}
	if wakes != 0 {
		t.Fatalf("onPackageUninstalling() woke the listener without reading the event")
	}
}

func TestLifecycleSubscribesTheFirstOwnerAndReleasesTheStream(t *testing.T) {
	home := t.TempDir()
	owner := "S-1-5-21-7-8-9-1100"
	const packageName = "OpenAI.Codex_2p2nqsd0c76g0_1.0.0.0_x64__abc"
	stream := &packageUninstallSubscription{}
	var subscriptions int
	var subscribedOwner string
	var subscribedFlag *atomic.Bool
	lifecycle := &Lifecycle{
		packageName: packageName,
		subscribe: func(gotPackage, ownerSID string, uninstalling *atomic.Bool, _ windows.Token) (*packageUninstallSubscription, error) {
			if gotPackage != packageName {
				t.Errorf("subscription package = %q, want %q", gotPackage, packageName)
			}
			subscribedOwner = ownerSID
			subscribedFlag = uninstalling
			subscriptions++
			return stream, nil
		},
		loadRecord: func() (*windowssandbox.InstallationRecord, error) { return nil, nil },
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			return record, nil
		},
	}
	if _, err := lifecycle.RegisterOwner(&windowssandbox.InstallationRecord{
		UserSID:   owner,
		CodexHome: home,
		SessionID: 1,
	}, openTestToken(t), windowssandbox.SetupRuntimeLegacy); err != nil {
		t.Fatalf("RegisterOwner() error = %v", err)
	}
	if subscriptions != 1 || subscribedOwner != owner {
		t.Fatalf("subscriptions = %d for owner %q, want one for %q", subscriptions, subscribedOwner, owner)
	}
	if subscribedFlag != lifecycle.Uninstalling() {
		t.Fatal("subscription did not share the lifecycle's uninstalling flag")
	}
	// A restored watcher keeps the stream it already has.
	if _, err := lifecycle.RegisterOwner(&windowssandbox.InstallationRecord{
		UserSID:   owner,
		CodexHome: home,
		SessionID: 2,
	}, openTestToken(t), windowssandbox.SetupRuntimeLegacy); err != nil {
		t.Fatalf("second RegisterOwner() error = %v", err)
	}
	if subscriptions != 1 {
		t.Fatalf("subscriptions = %d after a restored watcher, want 1", subscriptions)
	}
	lifecycle.Close()
	if !stream.closed.Load() {
		t.Fatal("Close() kept the package uninstall registration")
	}
}

func TestLifecycleRefusesRegistrationWithoutAnUninstallStream(t *testing.T) {
	home := t.TempDir()
	failure := errors.New("the package catalog is closed")
	lifecycle := &Lifecycle{
		subscribe: func(string, string, *atomic.Bool, windows.Token) (*packageUninstallSubscription, error) {
			return nil, failure
		},
		loadRecord: func() (*windowssandbox.InstallationRecord, error) { return nil, nil },
		saveRecord: func(record *windowssandbox.InstallationRecord) (*windowssandbox.InstallationRecord, error) {
			return record, nil
		},
	}
	t.Cleanup(lifecycle.Close)
	_, err := lifecycle.RegisterOwner(&windowssandbox.InstallationRecord{
		UserSID:   "S-1-5-21-1-2-3-1000",
		CodexHome: home,
	}, openTestToken(t), windowssandbox.SetupRuntimeLegacy)
	if err == nil || !IsServiceUnavailable(err) {
		t.Fatalf("RegisterOwner() error = %v, want a ServiceUnavailable refusal", err)
	}
	if !strings.Contains(err.Error(), "subscribe to authenticated package uninstall notifications") {
		t.Fatalf("RegisterOwner() error = %v", err)
	}
	if !strings.Contains(err.Error(), "the package catalog is closed") {
		t.Fatalf("RegisterOwner() error = %v", err)
	}
	if lifecycle.CodexHome() != "" {
		t.Fatalf("RegisterOwner() kept the pinned home %q after a failed subscription", lifecycle.CodexHome())
	}
}

func TestTypedEventHandlerDispatchesInvokeAndRefCounts(t *testing.T) {
	var delivered []uintptr
	handler, err := newTypedEventHandler(func(sender uintptr, args uintptr) error {
		delivered = append(delivered, sender+args)
		return nil
	})
	if err != nil {
		t.Fatalf("newTypedEventHandler() error = %v", err)
	}
	defer handler.release()

	var queried uintptr
	if status := typedEventHandlerQueryInterface(handler.pointer, 0, uintptr(unsafe.Pointer(&queried))); status != 0 || queried != handler.pointer {
		t.Fatalf("QueryInterface() = %d with %#x, want the same object", status, queried)
	}
	if got := handler.refs.Load(); got != 2 {
		t.Fatalf("QueryInterface() left %d references, want 2", got)
	}
	if got := typedEventHandlerAddRefX(handler.pointer); got != 3 {
		t.Fatalf("AddRef() = %d, want 3", got)
	}
	if got := typedEventHandlerRelease(handler.pointer); got != 2 {
		t.Fatalf("Release() = %d, want 2", got)
	}

	invoke := comMethod(handler.pointer, typedEventHandlerInvoke)
	status, _, _ := syscall.SyscallN(invoke, handler.pointer, 7, 11)
	if status != 0 {
		t.Fatalf("Invoke() status = %#x", status)
	}
	if len(delivered) != 1 || delivered[0] != 18 {
		t.Fatalf("Invoke() delivered %v, want one sender+args of 18", delivered)
	}
	if got := typedEventHandlerRelease(handler.pointer); got != 1 {
		t.Fatalf("Release() = %d, want 1", got)
	}
}
