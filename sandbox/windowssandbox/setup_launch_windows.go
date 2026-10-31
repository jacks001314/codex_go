//go:build windows

package windowssandbox

import (
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

const (
	createSuspended = 0x0000_0004
)

var (
	procNtResumeProcess       = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")
	procRtlNtStatusToDosError = windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlNtStatusToDosError")
)

// StartWithRetainedHandles starts the setup helper suspended, duplicates every
// retained directory handle into the child and only then resumes it, so the
// helper keeps those directories pinned until it exits independently of the
// service lifetime (Rust setup_launch::spawn_with_retained_handles).
func StartWithRetainedHandles(command *exec.Cmd, retained []windows.Handle) error {
	if command == nil {
		return ErrInvalidRequest
	}
	if command.SysProcAttr == nil {
		command.SysProcAttr = &syscall.SysProcAttr{}
	}
	command.SysProcAttr.CreationFlags |= createNoWindow | createSuspended
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	if err := command.Start(); err != nil {
		return err
	}
	if err := installRetainedHandles(command, retained); err != nil {
		// A partially initialized helper must never run after its parent drops
		// the directory protections.
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		return err
	}
	return nil
}

func installRetainedHandles(command *exec.Cmd, retained []windows.Handle) error {
	child, err := windows.OpenProcess(windows.PROCESS_DUP_HANDLE|windows.PROCESS_SUSPEND_RESUME, false, uint32(command.Process.Pid))
	if err != nil {
		return fmt.Errorf("open the setup helper process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(child) }()
	current := windows.CurrentProcess()
	for _, handle := range retained {
		var duplicate windows.Handle
		if err := windows.DuplicateHandle(current, handle, child, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return fmt.Errorf("retain a sandbox directory in the setup helper: %w", err)
		}
		// The duplicate belongs to the child's handle table; it is closed when
		// the helper exits and must not be closed here.
	}
	status, _, _ := procNtResumeProcess.Call(uintptr(child))
	if int32(status) < 0 {
		code, _, _ := procRtlNtStatusToDosError.Call(status)
		return fmt.Errorf("resume the setup helper: %w", syscall.Errno(code))
	}
	return nil
}
