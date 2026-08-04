//go:build windows

package gitutil

import (
	"fmt"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

const jobObjectLimitKillOnJobClose = 0x2000

var procNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

type gitProcessTree struct {
	cmd *exec.Cmd
	job windows.Handle
}

func startGitTree(cmd *exec.Cmd, respawn func() *exec.Cmd) (*gitProcessTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: jobObjectLimitKillOnJobClose,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("set job object limit: %w", err)
	}
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	processHandle, err := openGitProcessHandle(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(processHandle)
	if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		windows.CloseHandle(job)
		// Fall back to a plain spawn like Rust when job containment fails.
		retry := respawn()
		if retryErr := retry.Start(); retryErr != nil {
			return nil, retryErr
		}
		return &gitProcessTree{cmd: retry, job: 0}, nil
	}
	if err := resumeSuspendedProcess(processHandle); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		windows.CloseHandle(job)
		return nil, err
	}
	return &gitProcessTree{cmd: cmd, job: job}, nil
}

func openGitProcessHandle(pid int) (windows.Handle, error) {
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false,
		uint32(pid),
	)
	if err != nil {
		return 0, fmt.Errorf("open git process: %w", err)
	}
	return handle, nil
}

func (t *gitProcessTree) wait() error {
	if t == nil {
		return nil
	}
	err := t.cmd.Wait()
	if t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
	return err
}

func (t *gitProcessTree) kill() {
	if t == nil {
		return
	}
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
		windows.CloseHandle(t.job)
		t.job = 0
		return
	}
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
}

func resumeSuspendedProcess(handle windows.Handle) error {
	status, _, callErr := procNtResumeProcess.Call(uintptr(handle))
	if status != 0 {
		return fmt.Errorf("NtResumeProcess failed with NTSTATUS %#x: %v", status, callErr)
	}
	return nil
}
