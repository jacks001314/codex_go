package appserverdaemon

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func deepUpdaterDaemon(t *testing.T) *Daemon {
	t.Helper()
	home := filepath.Join(t.TempDir(), strings.Repeat("x", 120), "codex-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll(home) error = %v", err)
	}
	return NewDaemonForCodexHome(home, "codex-go-test")
}

func TestManualUpdaterSocketUsesPrivateTargetForLongPathLikeRust(t *testing.T) {
	daemon := deepUpdaterDaemon(t)
	socketPath := manualUpdateSocketPath(daemon)
	probeTarget := filepath.Join(t.TempDir(), "probe")
	probeLink := filepath.Join(filepath.Dir(socketPath), "probe-link")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(socket parent) error = %v", err)
	}
	if err := os.WriteFile(probeTarget, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(probe target) error = %v", err)
	}
	if err := os.Symlink(probeTarget, probeLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_ = os.Remove(probeLink)

	socket, err := bindUnixManualUpdaterSocket(socketPath)
	if err != nil {
		t.Fatalf("bindUnixManualUpdaterSocket() error = %v", err)
	}
	if socket == nil {
		t.Fatal("listenManualUpdateSocket() = nil")
	}
	advertised := socket.Path
	if len(advertised) <= 108 {
		t.Fatalf("advertised path %q (%d bytes) does not exercise sun_path", advertised, len(advertised))
	}
	info, err := os.Lstat(advertised)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("advertised updater path is not a symlink: info=%v err=%v", info, err)
	}
	target, err := filepath.EvalSymlinks(advertised)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) error = %v", advertised, err)
	}
	if len(target) > 108 {
		t.Fatalf("physical updater socket path %q (%d bytes) exceeds sun_path", target, len(target))
	}

	accepted := make(chan struct{}, 1)
	go func() {
		conn, acceptErr := socket.Listener.Accept()
		if acceptErr == nil {
			accepted <- struct{}{}
			_ = conn.Close()
		}
	}()
	connectPath := advertised
	if runtime.GOOS == "windows" {
		connectPath = target
	}
	conn, err := connectManualUpdaterSocket(connectPath, time.Second)
	if err != nil {
		t.Fatalf("connectManualUpdaterSocket(%q) error = %v", advertised, err)
	}
	_ = conn.Close()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("manual updater client did not reach the private listener")
	}

	if err := socket.Close(); err != nil {
		t.Fatalf("socket.Close() error = %v", err)
	}
	if _, err := os.Lstat(advertised); !os.IsNotExist(err) {
		t.Fatalf("advertised updater socket survived cleanup: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("physical updater socket survived cleanup: %v", err)
	}
}

func TestManualUpdaterSocketPreservesRegularFileLikeRust(t *testing.T) {
	daemon := deepUpdaterDaemon(t)
	socketPath := manualUpdateSocketPath(daemon)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(socket parent) error = %v", err)
	}
	if err := os.WriteFile(socketPath, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("WriteFile(socket path) error = %v", err)
	}
	if socket, err := bindUnixManualUpdaterSocket(socketPath); err == nil || socket != nil {
		t.Fatalf("bindUnixManualUpdaterSocket() = %#v, %v; want a preserved-file error", socket, err)
	}
	contents, err := os.ReadFile(socketPath)
	if err != nil || string(contents) != "not a socket" {
		t.Fatalf("regular updater path changed: contents=%q err=%v", contents, err)
	}
}

func TestManagedUpdaterSocketReadinessChecksProcessAndSocketLikeRust(t *testing.T) {
	socketDir, err := os.MkdirTemp("", "updready")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	socketPath := filepath.Join(socketDir, "updater.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.Remove(socketDir)
		t.Skipf("unix socket unavailable: %v", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		_ = os.Remove(socketDir)
	}()

	running := func(*PIDBackend) (bool, error) { return true, nil }
	if err := waitForManagedUpdaterSocketReadyUnix(nil, socketPath, running); err != nil {
		t.Fatalf("ready socket error = %v", err)
	}
	if err := waitForManagedUpdaterSocketReadyUnix(nil, socketPath, func(*PIDBackend) (bool, error) { return false, nil }); err == nil || !strings.Contains(err.Error(), "exited before becoming ready") {
		t.Fatalf("exited updater error = %v", err)
	}

	previousTimeout := managedUpdaterReadyTimeout
	previousPoll := managedUpdaterReadyPollInterval
	managedUpdaterReadyTimeout = 20 * time.Millisecond
	managedUpdaterReadyPollInterval = 5 * time.Millisecond
	defer func() {
		managedUpdaterReadyTimeout = previousTimeout
		managedUpdaterReadyPollInterval = previousPoll
	}()
	if err := waitForManagedUpdaterSocketReadyUnix(nil, filepath.Join(t.TempDir(), "missing.sock"), running); err == nil || !strings.Contains(err.Error(), "timed out before becoming ready") {
		t.Fatalf("missing-socket error = %v", err)
	}
}
