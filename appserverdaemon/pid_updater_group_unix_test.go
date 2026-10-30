//go:build !windows

package appserverdaemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestStoppingUpdaterSignalsItsInstallerProcessGroup mirrors Rust #43542: the
// updater's graceful stop signals its process group so an in-flight installer
// child is terminated with it.
func TestStoppingUpdaterSignalsItsInstallerProcessGroup(t *testing.T) {
	temp := t.TempDir()
	ready := filepath.Join(temp, "installer.ready")
	stopped := filepath.Join(temp, "installer.stopped")
	command := exec.Command("/bin/sh", "-c", `sh -c 'trap "touch $STOP_MARKER; exit 0" TERM; touch $READY_MARKER; while :; do sleep 1; done' & wait`)
	command.Env = append(os.Environ(), "READY_MARKER="+ready, "STOP_MARKER="+stopped)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		t.Fatalf("start updater shim: %v", err)
	}
	pid := uint32(command.Process.Pid)
	// The shim exits once the group is signalled; reap it during cleanup.
	t.Cleanup(func() { _ = command.Wait() })
	waitForFile(t, ready, "installer did not start")
	startTime, err := readPIDProcessStartTime(pid)
	if err != nil {
		t.Fatalf("read updater start time: %v", err)
	}
	pidFile := filepath.Join(temp, "updater.pid")
	if err := WritePIDRecord(pidFile, &PIDRecord{PID: pid, ProcessStartTime: startTime}); err != nil {
		t.Fatalf("WritePIDRecord: %v", err)
	}
	backend := NewPIDUpdateLoopBackend(BackendPaths{PIDFile: pidFile})
	if err := backend.Stop(); err != nil {
		t.Fatalf("stop updater: %v", err)
	}
	waitForFile(t, stopped, "installer child was not stopped with the updater group")
}

func waitForFile(t *testing.T, path string, message string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(message)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
