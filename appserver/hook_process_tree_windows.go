//go:build windows

package appserver

import (
	"fmt"
	osexec "os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

const hookJobObjectLimitKillOnJobClose = 0x2000

var procHookNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// hookProcessTree owns a Windows Job Object used to terminate a hook's whole
// process tree on timeout or cancellation (Rust dd916428cd).
type hookProcessTree struct {
	cmd              *osexec.Cmd
	job              windows.Handle
	preserveDescends bool
}

func startHookProcessTree(cmd *osexec.Cmd) (*hookProcessTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create hook job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: hookJobObjectLimitKillOnJobClose,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("set hook job object limit: %w", err)
	}
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	processHandle, err := openHookProcessHandle(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		windows.CloseHandle(job)
		return nil, err
	}
	defer windows.CloseHandle(processHandle)
	if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
		windows.CloseHandle(job)
		// The process is already running; fall back to taskkill-based tree
		// termination like Rust's ProcessTreeGuard when job containment fails.
		return &hookProcessTree{cmd: cmd}, nil
	}
	if err := resumeHookSuspendedProcess(processHandle); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		windows.CloseHandle(job)
		return nil, err
	}
	return &hookProcessTree{cmd: cmd, job: job}, nil
}

func openHookProcessHandle(pid int) (windows.Handle, error) {
	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false,
		uint32(pid),
	)
	if err != nil {
		return 0, fmt.Errorf("open hook process: %w", err)
	}
	return handle, nil
}

func resumeHookSuspendedProcess(handle windows.Handle) error {
	status, _, callErr := procHookNtResumeProcess.Call(uintptr(handle))
	if status != 0 {
		if callErr != nil {
			return fmt.Errorf("resume suspended hook process: %v", callErr)
		}
		return fmt.Errorf("resume suspended hook process: NTSTATUS %#x", uint32(status))
	}
	return nil
}

func (t *hookProcessTree) wait() error {
	if t == nil || t.cmd == nil {
		return nil
	}
	return t.cmd.Wait()
}

func (t *hookProcessTree) terminate() {
	if t == nil {
		return
	}
	if t.job != 0 && !t.preserveDescends {
		_ = windows.TerminateJobObject(t.job, 1)
		windows.CloseHandle(t.job)
		t.job = 0
		return
	}
	if t.job != 0 {
		windows.CloseHandle(t.job)
		t.job = 0
	}
	if t.cmd != nil && t.cmd.Process != nil {
		// Rust fallback: taskkill /PID <pid> /T /F terminates the whole tree.
		_ = osexec.Command("taskkill", "/PID", fmt.Sprintf("%d", t.cmd.Process.Pid), "/T", "/F").Run()
	}
}

// preserveDescendants allows contained descendants to keep running after the
// hook completes successfully (Rust JobObject::preserve_descendants).
func (t *hookProcessTree) preserveDescendants() {
	if t == nil || t.job == 0 || t.preserveDescends {
		return
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: 0,
		},
	}
	if _, err := windows.SetInformationJobObject(t.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err == nil {
		t.preserveDescends = true
	}
}
