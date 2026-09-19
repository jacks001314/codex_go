//go:build windows

package windowssandbox

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestProcThreadAttributeListSetHandleList(t *testing.T) {
	attrs, err := NewProcThreadAttributeListWithCount(1)
	if err != nil {
		t.Fatalf("NewProcThreadAttributeListWithCount() error = %v", err)
	}
	defer attrs.Close()
	if attrs.WindowsList() == nil {
		t.Fatalf("WindowsList() = nil")
	}
	if err := attrs.SetHandleList([]uintptr{uintptr(windows.CurrentProcess())}); err != nil {
		t.Fatalf("SetHandleList() error = %v", err)
	}
}

func TestProcThreadAttributeListRejectsZeroCount(t *testing.T) {
	if _, err := NewProcThreadAttributeListWithCount(0); err == nil {
		t.Fatalf("NewProcThreadAttributeListWithCount(0) error = nil, want error")
	}
}

// Mirrors Rust #46575's preserve_desktop_app_context: the attribute list accepts
// the desktop app policy alongside the stdio handle list, and the policy keeps
// the child and its descendants inside the caller's package environment.
func TestProcThreadAttributeListPreservesDesktopAppContext(t *testing.T) {
	if got := ProcThreadAttributeCountForLaunch(false); got != 1 {
		t.Fatalf("attribute count without package identity = %d, want 1", got)
	}
	if got := ProcThreadAttributeCountForLaunch(true); got != 2 {
		t.Fatalf("attribute count with package identity = %d, want 2", got)
	}
	attrs, err := NewProcThreadAttributeListWithCount(ProcThreadAttributeCountForLaunch(true))
	if err != nil {
		t.Fatalf("NewProcThreadAttributeListWithCount() error = %v", err)
	}
	defer attrs.Close()
	if err := attrs.SetHandleList([]uintptr{uintptr(windows.CurrentProcess())}); err != nil {
		t.Fatalf("SetHandleList() error = %v", err)
	}
	if err := attrs.PreserveDesktopAppContext(); err != nil {
		t.Fatalf("PreserveDesktopAppContext() error = %v", err)
	}
	want := uint32(processCreationDesktopAppBreakawayDisableProcessTree | processCreationDesktopAppBreakawayOverride)
	if attrs.desktopAppPolicy != want {
		t.Fatalf("desktop app policy = %d, want %d", attrs.desktopAppPolicy, want)
	}
}

// The current process's OS package identity decides whether desktop app context
// is preserved; an unpackaged host reports no identity without failing.
func TestCurrentProcessHasPackageIdentityReadsTheOsPackageState(t *testing.T) {
	name, has, err := PackageFullNameForProcess(windows.CurrentProcess())
	if err != nil {
		t.Fatalf("PackageFullNameForProcess() error = %v", err)
	}
	if has && name == "" {
		t.Fatal("a package identity must report a non-empty full name")
	}
	if !has && RegisteredCoreRequested() {
		t.Skip("host requests registered-core execution without a package identity")
	}
	got, err := CurrentProcessHasPackageIdentity()
	if err != nil {
		t.Fatalf("CurrentProcessHasPackageIdentity() error = %v", err)
	}
	if got != has {
		t.Fatalf("CurrentProcessHasPackageIdentity() = %v, want the queried %v", got, has)
	}
}
