//go:build !windows

package appserverdaemon

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestReservationProbeReleasesLockWithDuplicatedHandle pins Rust #57512/#57533:
// a probe must release its lock before returning so a file description shared
// with a concurrent fork cannot keep the daemon looking reserved.
func TestReservationProbeReleasesLockWithDuplicatedHandle(t *testing.T) {
	backend := NewPIDBackend(BackendPaths{PIDFile: filepath.Join(t.TempDir(), PIDFileName)})
	file, err := openLockFile(backend.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// Dup shares the open file description, just as an inherited descriptor
	// would after fork.
	inherited, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(inherited)

	active, err := fileLockIsActive(backend.LockFile)
	if err != nil {
		t.Fatalf("fileLockIsActive() error = %v", err)
	}
	if active {
		t.Fatal("unlocked reservation probe reported active")
	}
	competitor, err := openLockFile(backend.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	defer competitor.Close()
	if err := syscall.Flock(int(competitor.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("inherited descriptor kept the probe lock: %v", err)
	}
	if err := syscall.Flock(int(competitor.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
}

// TestHeldReservationWithoutPIDFileReportsStarting pins the second half of
// Rust #57512: a live reservation is Starting even before the PID file exists.
func TestHeldReservationWithoutPIDFileReportsStarting(t *testing.T) {
	backend := NewPIDBackend(BackendPaths{PIDFile: filepath.Join(t.TempDir(), PIDFileName)})
	lock, err := acquireExclusiveFileLock(backend.LockFile, time.Second, 10*time.Millisecond, "pid lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := os.Stat(backend.PIDFile); !os.IsNotExist(err) {
		t.Fatalf("PID file exists before reservation completes: %v", err)
	}
	running, err := backend.IsStartingOrRunning()
	if err != nil {
		t.Fatalf("IsStartingOrRunning() error = %v", err)
	}
	if !running {
		t.Fatal("held reservation without a PID file was not reported as starting")
	}
}

func TestPIDBackendStartStopUnixProcess(t *testing.T) {
	script := writeLongRunningCodexScript(t)
	backend := NewPIDBackend(BackendPaths{
		CodexBin:             script,
		PIDFile:              filepath.Join(t.TempDir(), PIDFileName),
		RemoteControlEnabled: true,
	})

	pid, err := backend.Start()
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if pid == nil || *pid == 0 {
		t.Fatalf("pid = %#v", pid)
	}
	running, err := backend.IsStartingOrRunning()
	if err != nil {
		t.Fatalf("IsStartingOrRunning error = %v", err)
	}
	if !running {
		t.Fatal("backend is not running after start")
	}
	if err := backend.Stop(); err != nil {
		t.Fatalf("Stop error = %v", err)
	}
	running, err = backend.IsStartingOrRunning()
	if err != nil {
		t.Fatalf("IsStartingOrRunning after stop error = %v", err)
	}
	if running {
		t.Fatal("backend still running after stop")
	}
}

func TestPIDBackendCleansStalePIDRecord(t *testing.T) {
	backend := NewPIDBackend(BackendPaths{PIDFile: filepath.Join(t.TempDir(), PIDFileName)})
	record := &PIDRecord{PID: uint32(os.Getpid()), ProcessStartTime: "definitely-not-this-process-start"}
	if err := WritePIDRecord(backend.PIDFile, record); err != nil {
		t.Fatalf("WritePIDRecord error = %v", err)
	}

	running, err := backend.IsStartingOrRunning()
	if err != nil {
		t.Fatalf("IsStartingOrRunning error = %v", err)
	}
	if running {
		t.Fatal("stale pid record reported running")
	}
	if _, err := os.Stat(backend.PIDFile); !os.IsNotExist(err) {
		t.Fatalf("pid file stat error = %v, want not exist", err)
	}
}

func TestLifecycleRunnerTryRestartReportsBusyWhenOperationLocked(t *testing.T) {
	daemon := NewDaemonForCodexHome(t.TempDir(), "codex-go-test")
	lock, err := acquireExclusiveFileLock(daemon.Paths.OperationLockFile, time.Second, time.Millisecond, "daemon operation lock")
	if err != nil {
		t.Fatalf("acquire lock error = %v", err)
	}
	defer lock.Close()

	outcome, err := NewLifecycleRunner(daemon).TryRestartIfRunning(RestartAlways, UpdaterRefreshNone, daemon.Paths.ManagedCodexBin)
	if err != nil {
		t.Fatalf("TryRestartIfRunning error = %v", err)
	}
	if outcome != RestartBusy {
		t.Fatalf("outcome = %s, want %s", outcome, RestartBusy)
	}
}

func writeLongRunningCodexScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\ntrap 'exit 0' TERM\nwhile true; do sleep 1; done\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile script error = %v", err)
	}
	return path
}
