//go:build windows

package appserverdaemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsDaemonShutdownWatcherConsumesMatchingPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.shutdown")
	t.Setenv(DaemonShutdownFileEnv, path)
	daemonShutdownPollInterval = 5 * time.Millisecond

	backend := NewPIDBackend(BackendPaths{
		CodexBin: os.Args[0],
		PIDFile:  filepath.Join(dir, "daemon.pid"),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		WatchDaemonShutdownRequest(ctx, cancel)
		close(done)
	}()
	record := &PIDRecord{PID: uint32(os.Getpid()), ProcessStartTime: "test"}
	if err := requestGracefulPIDShutdown(backend, record); err != nil {
		t.Fatalf("requestGracefulPIDShutdown error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("watcher did not cancel on matching PID")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("shutdown file was not consumed after matching request")
	}
}

func TestWindowsDaemonShutdownWatcherIgnoresOtherPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon-other.shutdown")
	t.Setenv(DaemonShutdownFileEnv, path)
	daemonShutdownPollInterval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		WatchDaemonShutdownRequest(ctx, cancel)
		close(done)
	}()
	if err := os.WriteFile(path, []byte(`{"pid":1}`), 0o600); err != nil {
		t.Fatalf("write shutdown request error = %v", err)
	}
	select {
	case <-done:
		t.Fatalf("watcher consumed a request for another PID")
	case <-time.After(60 * time.Millisecond):
	}
}
