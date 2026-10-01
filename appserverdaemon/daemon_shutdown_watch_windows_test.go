//go:build windows

package appserverdaemon

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestWindowsDaemonShutdownWatcherConsumesMatchingPID pins the updater's
// shutdown signal: the file holds the plain decimal PID, and only the addressed
// process consumes it (Rust daemon_shutdown::take_shutdown_request).
func TestWindowsDaemonShutdownWatcherConsumesMatchingPID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.shutdown")
	t.Setenv(DaemonShutdownFileEnv, path)
	daemonShutdownPollInterval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		WatchDaemonShutdownRequest(ctx, cancel)
		close(done)
	}()
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("write shutdown request error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not cancel on matching PID")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("shutdown file was not consumed after matching request")
	}
}

// TestWindowsDaemonShutdownWatcherIgnoresOtherRequests pins that another
// process's request — and the older JSON shape — never stop this one.
func TestWindowsDaemonShutdownWatcherIgnoresOtherRequests(t *testing.T) {
	for _, contents := range []string{"1", `{"pid":1}`} {
		path := filepath.Join(t.TempDir(), "daemon-other.shutdown")
		t.Setenv(DaemonShutdownFileEnv, path)
		daemonShutdownPollInterval = 5 * time.Millisecond

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			WatchDaemonShutdownRequest(ctx, cancel)
			close(done)
		}()
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write shutdown request error = %v", err)
		}
		select {
		case <-done:
			t.Fatalf("watcher consumed %q", contents)
		case <-time.After(60 * time.Millisecond):
		}
		cancel()
		<-done
	}
}

// TestRequestGracefulPIDShutdownBranchesLikeRust pins the two Windows shutdown
// paths: a managed app-server is asked over its control socket, while the
// updater receives a plain-PID file beside its pid file.
func TestRequestGracefulPIDShutdownBranchesLikeRust(t *testing.T) {
	original := requestDaemonShutdown
	t.Cleanup(func() { requestDaemonShutdown = original })
	dir := t.TempDir()

	var gotSocket string
	var gotPID uint32
	requestDaemonShutdown = func(socketPath string, pid uint32, _ time.Duration) error {
		gotSocket = socketPath
		gotPID = pid
		return nil
	}
	appServer := NewPIDBackend(BackendPaths{
		CodexBin: os.Args[0],
		PIDFile:  filepath.Join(dir, StateDirName, "daemon.pid"),
	})
	if err := requestGracefulPIDShutdown(appServer, &PIDRecord{PID: 4321}); err != nil {
		t.Fatalf("requestGracefulPIDShutdown(app-server) error = %v", err)
	}
	if gotPID != 4321 || gotSocket != AppServerControlSocketPath(dir) {
		t.Fatalf("socket request = %q pid=%d", gotSocket, gotPID)
	}
	if _, err := os.Stat(daemonShutdownFilePath(appServer.PIDFile)); !os.IsNotExist(err) {
		t.Fatal("the app-server path must not write a shutdown file")
	}

	updater := NewPIDUpdateLoopBackend(BackendPaths{
		CodexBin:      os.Args[0],
		UpdatePIDFile: filepath.Join(dir, StateDirName, "daemon-updater.pid"),
	})
	gotSocket = ""
	gotPID = 0
	if err := requestGracefulPIDShutdown(updater, &PIDRecord{PID: 555}); err != nil {
		t.Fatalf("requestGracefulPIDShutdown(updater) error = %v", err)
	}
	if gotPID != 0 || gotSocket != "" {
		t.Fatalf("the updater must not use the control socket: %q pid=%d", gotSocket, gotPID)
	}
	contents, err := os.ReadFile(daemonShutdownFilePath(updater.PIDFile))
	if err != nil {
		t.Fatalf("read updater shutdown request error = %v", err)
	}
	if string(contents) != "555" {
		t.Fatalf("updater shutdown request = %q, want the plain PID", string(contents))
	}
}
