//go:build windows

package windowssandbox

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrepareLaunchDesktopDefault(t *testing.T) {
	desktop, err := PrepareLaunchDesktop(false)
	if err != nil {
		t.Fatalf("PrepareLaunchDesktop(false) error = %v", err)
	}
	if desktop.StartupName != `Winsta0\Default` {
		t.Fatalf("StartupName = %q, want default desktop", desktop.StartupName)
	}
}

func TestCreateLaunchDesktopRejectsEmptyName(t *testing.T) {
	if _, err := CreateLaunchDesktop(""); err == nil {
		t.Fatalf("CreateLaunchDesktop(\"\") error = nil, want error")
	}
}

func TestPrepareLaunchDesktopPrivateCreatesAndCloses(t *testing.T) {
	desktop, err := PrepareLaunchDesktop(true)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Skipf("host does not allow private desktop creation: %v", err)
		}
		t.Fatalf("PrepareLaunchDesktop(true) error = %v", err)
	}
	if desktop.Handle == 0 {
		t.Fatalf("private desktop handle is zero")
	}
	if desktop.StartupName == "" || desktop.StartupName == `Winsta0\Default` {
		t.Fatalf("private StartupName = %q", desktop.StartupName)
	}
	if err := desktop.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
