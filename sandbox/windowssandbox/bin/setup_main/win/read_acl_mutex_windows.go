//go:build windows

package win

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

const mutexAllAccess = 0x001F0001

func ReadACLMutexExists() (bool, error) {
	name, err := windows.UTF16PtrFromString(ReadACLMutexName)
	if err != nil {
		return false, err
	}
	handle, err := windows.OpenMutex(mutexAllAccess, false, name)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return false, nil
		}
		return false, err
	}
	_ = windows.CloseHandle(handle)
	return true, nil
}

func AcquireReadACLMutex() (*ReadACLMutexGuard, bool, error) {
	name, err := windows.UTF16PtrFromString(ReadACLMutexName)
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateMutex(nil, true, name)
	if err != nil {
		if errno, ok := err.(syscall.Errno); ok && errno == windows.ERROR_ALREADY_EXISTS {
			if handle != 0 {
				_ = windows.CloseHandle(handle)
			}
			return nil, false, nil
		}
		return nil, false, err
	}
	return &ReadACLMutexGuard{handle: uintptr(handle)}, true, nil
}

func (g *ReadACLMutexGuard) Close() error {
	if g == nil || g.handle == 0 {
		return nil
	}
	handle := windows.Handle(g.handle)
	g.handle = 0
	_ = windows.ReleaseMutex(handle)
	return windows.CloseHandle(handle)
}
