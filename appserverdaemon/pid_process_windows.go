//go:build windows

package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// startDetachedPIDProcess launches the managed app-server as a detached
// Windows process (Rust #42381/#42405). The child must be able to break away
// from the launching job, must not run in the launching project's working
// directory, and must not be started from an elevated parent.
func startDetachedPIDProcess(backend *PIDBackend) (uint32, string, error) {
	if backend == nil {
		return 0, "", fmt.Errorf("pid backend is nil")
	}
	if windows.GetCurrentProcessToken().IsElevated() {
		return 0, "", fmt.Errorf("pid-managed app-server startup requires a non-elevated Windows process")
	}
	if err := os.MkdirAll(filepath.Dir(backend.PIDFile), 0o700); err != nil {
		return 0, "", fmt.Errorf("failed to create pid directory %s: %w", filepath.Dir(backend.PIDFile), err)
	}
	command := exec.Command(backend.CodexBin, backend.CommandArgs()...)
	workingDir := filepath.Dir(backend.PIDFile)
	if workingDir == "" {
		workingDir = "."
	}
	command.Dir = workingDir
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB,
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return 0, "", err
	}
	defer devNull.Close()
	stderrLog, err := os.OpenFile(backend.StderrLogPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, "", fmt.Errorf("failed to open stderr log for pid-managed app server %s: %w", backend.StderrLogPath(), err)
	}
	defer stderrLog.Close()
	command.Stdin = devNull
	command.Stdout = devNull
	command.Stderr = stderrLog
	if env := backend.CommandEnv(); len(env) > 0 {
		command.Env = os.Environ()
		for key, value := range env {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	if err := command.Start(); err != nil {
		return 0, "", fmt.Errorf("failed to spawn detached app-server process using %s: %w", backend.CodexBin, err)
	}
	pid := uint32(command.Process.Pid)
	processStartTime, err := readPIDProcessStartTime(pid)
	if err != nil {
		_ = terminatePIDProcess(pid)
		_ = command.Process.Release()
		return 0, "", fmt.Errorf("failed to record pid-managed app-server process %d startup: %w", pid, err)
	}
	_ = command.Process.Release()
	return pid, processStartTime, nil
}

func processMatchesPIDRecord(record *PIDRecord) (bool, error) {
	if record == nil || record.PID == 0 {
		return false, nil
	}
	if !pidProcessExists(record.PID) {
		return false, nil
	}
	startTime, err := readPIDProcessStartTime(record.PID)
	if err != nil {
		if !pidProcessExists(record.PID) {
			return false, nil
		}
		return false, err
	}
	return startTime == record.ProcessStartTime, nil
}

func terminatePIDProcess(pid uint32) error {
	return windowsTerminatePIDProcess(pid, false)
}

func forceTerminatePIDProcess(pid uint32, _ bool) error {
	return windowsTerminatePIDProcess(pid, true)
}

func windowsTerminatePIDProcess(pid uint32, force bool) error {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return fmt.Errorf("failed to open pid-managed app server %d for termination: %w", pid, err)
	}
	defer windows.CloseHandle(handle)
	if err := windows.TerminateProcess(handle, 1); err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		kind := "app server"
		if force {
			kind = "app server (forced)"
		}
		return fmt.Errorf("failed to terminate pid-managed %s %d: %w", kind, pid, err)
	}
	return nil
}

func pidProcessExists(pid uint32) bool {
	if pid == 0 {
		return false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	return true
}

// readPIDProcessStartTime returns a stable per-process creation timestamp that
// distinguishes PID reuse (Rust #42381). The FILETIME->UTC conversion has
// enough precision that a reused PID can never match the recorded value.
func readPIDProcessStartTime(pid uint32) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return "", nil
		}
		return "", fmt.Errorf("failed to open pid-managed app server %d: %w", pid, err)
	}
	defer windows.CloseHandle(handle)
	var creationTime windows.Filetime
	var exitTime windows.Filetime
	var kernelTime windows.Filetime
	var userTime windows.Filetime
	if err := windows.GetProcessTimes(handle, &creationTime, &exitTime, &kernelTime, &userTime); err != nil {
		return "", fmt.Errorf("failed to read creation time for pid-managed app server %d: %w", pid, err)
	}
	return time.Unix(0, creationTime.Nanoseconds()).UTC().Format(time.RFC3339Nano), nil
}
