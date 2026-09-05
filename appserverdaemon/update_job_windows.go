//go:build windows

package appserverdaemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

const updateJobObjectLimitKillOnJobClose = 0x2000

var procUpdateNtResumeProcess = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtResumeProcess")

// runWindowsUpdateInstaller runs the non-interactive Windows standalone
// installer inside a Job Object that kills any installer descendants when the
// updater exits (Rust #42392). The child starts suspended so it is assigned to
// the job before it can spawn grandchildren outside the job.
func runWindowsUpdateInstaller(ctx context.Context, command string, args []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return fmt.Errorf("resolve standalone Codex updater command %q: %w", command, err)
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create updater job object: %w", err)
	}
	defer windows.CloseHandle(job)
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: updateJobObjectLimitKillOnJobClose,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return fmt.Errorf("set updater job object limit: %w", err)
	}
	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.Env = appendExecutableEnvironment(updateInstallerEnvVars())
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("invoke standalone Codex updater: %w", err)
	}
	processHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_SUSPEND_RESUME,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("open updater process %d: %w", cmd.Process.Pid, err)
	}
	defer windows.CloseHandle(processHandle)
	if err := windows.AssignProcessToJobObject(job, processHandle); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("assign updater process %d to job: %w", cmd.Process.Pid, err)
	}
	if err := resumeUpdateInstallerProcess(processHandle); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

func resumeUpdateInstallerProcess(handle windows.Handle) error {
	status, _, callErr := procUpdateNtResumeProcess.Call(uintptr(handle))
	if status != 0 {
		if callErr != nil {
			return fmt.Errorf("resume updater process: %v", callErr)
		}
		return fmt.Errorf("resume updater process: NTSTATUS %#x", uint32(status))
	}
	return nil
}

func appendExecutableEnvironment(env map[string]string) []string {
	out := os.Environ()
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

func updateInstallerEnvVars() map[string]string {
	return map[string]string{"CODEX_NON_INTERACTIVE": "1"}
}
