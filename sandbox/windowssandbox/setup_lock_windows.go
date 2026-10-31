//go:build windows

package windowssandbox

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// sandboxSetupMutexName serializes sandbox setup and uninstall across helper
// processes (Rust setup_mutex::acquire_sandbox_setup_lock).
const sandboxSetupMutexName = `Global\CodexSandboxSetup`

const InfiniteSandboxSetupLockTimeout uint32 = 0xFFFFFFFF

// SandboxSetupLock serializes account provisioning and uninstall.
type SandboxSetupLock struct {
	handle windows.Handle
}

func AcquireSandboxSetupLock(timeoutMS uint32) (*SandboxSetupLock, error) {
	name, err := windows.UTF16PtrFromString(sandboxSetupMutexName)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return nil, fmt.Errorf("create sandbox setup mutex: %w", err)
	}
	event, err := windows.WaitForSingleObject(handle, timeoutMS)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("wait for sandbox setup mutex: %w", err)
	}
	switch event {
	case windows.WAIT_OBJECT_0, windows.WAIT_ABANDONED:
		return &SandboxSetupLock{handle: handle}, nil
	case uint32(windows.WAIT_TIMEOUT):
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("timed out waiting for sandbox setup mutex after %d ms", timeoutMS)
	default:
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("unexpected sandbox setup mutex wait result %d", event)
	}
}

func (l *SandboxSetupLock) Release() {
	if l == nil || l.handle == 0 {
		return
	}
	_ = windows.ReleaseMutex(l.handle)
	_ = windows.CloseHandle(l.handle)
	l.handle = 0
}
