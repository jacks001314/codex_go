//go:build windows

package windowssandbox

import (
	"errors"
	"strings"
	"testing"
)

func TestStartWithRetainedHandlesRequiresACommand(t *testing.T) {
	if err := StartWithRetainedHandles(nil, nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("StartWithRetainedHandles() error = %v, want ErrInvalidRequest", err)
	}
}

func TestIsLocalDiskPathRequiresADriveLetterRoot(t *testing.T) {
	for _, testCase := range []struct {
		path string
		want bool
	}{
		{path: `C:\Users\owner\.codex`, want: true},
		{path: `\\?\C:\Users\owner\.codex`, want: true},
		{path: `\\server\share\.codex`, want: false},
		{path: `.codex`, want: false},
		{path: "", want: false},
	} {
		if got := isLocalDiskPath(testCase.path); got != testCase.want {
			t.Fatalf("isLocalDiskPath(%q) = %t, want %t", testCase.path, got, testCase.want)
		}
	}
}

func TestRunElevatedProvisioningSetupWithRetainedHandlesRefusesANonLocalHome(t *testing.T) {
	err := RunElevatedProvisioningSetupWithRetainedHandles(
		`relative\home`,
		`DOMAIN\owner`,
		OfflineProxySettings{},
		SetupRuntimeLegacy,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "absolute local disk path") {
		t.Fatalf("RunElevatedProvisioningSetupWithRetainedHandles() error = %v", err)
	}
}
