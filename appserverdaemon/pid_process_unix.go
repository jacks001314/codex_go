//go:build !windows

package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func startDetachedPIDProcess(backend *PIDBackend) (uint32, string, error) {
	if backend == nil {
		return 0, "", fmt.Errorf("pid backend is nil")
	}
	command := exec.Command(backend.CodexBin, backend.CommandArgs()...)
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
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if env := backend.CommandEnv(); len(env) > 0 {
		command.Env = os.Environ()
		for key, value := range env {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	if command.Env == nil {
		command.Env = os.Environ()
	}
	command.Env = append(command.Env, UpdaterPIDFileEnv+"="+backend.PIDFile)
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
	err := syscall.Kill(int(pid), syscall.SIGTERM)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return fmt.Errorf("failed to terminate pid-managed app server %d: %w", pid, err)
}

func forceTerminatePIDProcess(pid uint32, processGroup bool) error {
	target := int(pid)
	if processGroup {
		target = -target
	}
	err := syscall.Kill(target, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if processGroup {
		return fmt.Errorf("failed to force terminate pid-managed updater group %d: %w", pid, err)
	}
	return fmt.Errorf("failed to force terminate pid-managed app server %d: %w", pid, err)
}

func pidProcessExists(pid uint32) bool {
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 2 && fields[2] == "Z" {
			return false
		}
	}
	err := syscall.Kill(int(pid), 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func readPIDProcessStartTime(pid uint32) (string, error) {
	output, err := exec.Command("ps", "-p", strconv.Itoa(int(pid)), "-o", "lstart=").Output()
	if err != nil {
		return "", fmt.Errorf("failed to read start time for pid-managed app server %d: %w", pid, err)
	}
	startTime := stringsTrimSpaceASCII(string(output))
	if startTime == "" {
		return "", fmt.Errorf("pid-managed app server %d has no recorded start time", pid)
	}
	return startTime, nil
}

func stringsTrimSpaceASCII(value string) string {
	for len(value) > 0 {
		switch value[0] {
		case ' ', '\t', '\r', '\n':
			value = value[1:]
		default:
			goto trimRight
		}
	}
trimRight:
	for len(value) > 0 {
		switch value[len(value)-1] {
		case ' ', '\t', '\r', '\n':
			value = value[:len(value)-1]
		default:
			return value
		}
	}
	return value
}
