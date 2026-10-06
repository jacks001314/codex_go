//go:build windows

package appserverdaemon

import (
	"bytes"
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
	return runWindowsUpdateInstallerWithInput(ctx, command, args, nil, updateInstallerEnvVars())
}

// runWindowsUpdateInstallerWithInput runs a prepared installer invocation
// inside the kill-on-close job, optionally feeding the fetched script on stdin
// (Rust run_installer_script: the guards travel as environment variables and the
// script itself arrives on stdin).
func runWindowsUpdateInstallerWithInput(ctx context.Context, command string, args []string, script []byte, env map[string]string) error {
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
	cmd.Env = appendExecutableEnvironment(env)
	if script != nil {
		cmd.Stdin = bytes.NewReader(script)
	}
	// Keep the last installer diagnostics so a failed run reports them, and
	// bound the drain so a descendant holding the pipe open cannot stall the
	// updater (Rust update_loop's bounded stderr drain, #50499).
	tail := &installerStderrTail{}
	cmd.Stderr = tail
	cmd.WaitDelay = installerStderrDrainTimeout
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
	if err := cmd.Wait(); err != nil {
		return installerExitError(err, tail)
	}
	return nil
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
