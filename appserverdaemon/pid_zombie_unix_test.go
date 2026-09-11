//go:build unix

package appserverdaemon

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// Mirrors Rust #43504 pid_tests.rs::exited_unreaped_updater_is_reaped: an
// exited, unreaped child still passes kill(pid, 0) and keeps its start time, so
// the PID backend must report it inactive and reap it. A zombie remains
// addressable by pid, so ESRCH after the check proves the process was reaped.
func TestExitedUnreapedChildIsInactiveAndReaped(t *testing.T) {
	command := exec.Command("sleep", "60")
	if err := command.Start(); err != nil {
		t.Fatalf("start shim error = %v", err)
	}
	pid := uint32(command.Process.Pid)
	startTime, err := readPIDProcessStartTime(pid)
	if err != nil {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		t.Fatalf("readPIDProcessStartTime error = %v", err)
	}
	record := &PIDRecord{PID: pid, ProcessStartTime: startTime}
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("kill shim error = %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		active, err := processMatchesPIDRecord(record)
		if err != nil {
			t.Fatalf("processMatchesPIDRecord error = %v", err)
		}
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("an exited, unreaped child was reported active")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := syscall.Kill(int(pid), 0); err != syscall.ESRCH {
		t.Fatalf("kill(pid, 0) after zombie reap = %v, want ESRCH", err)
	}
	// The child was reaped by the backend, so Wait reports an ECHILD-style error;
	// its exact error is not asserted here.
	_ = command.Wait()
}
