//go:build !windows

package appserverdaemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
