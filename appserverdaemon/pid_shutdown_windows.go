//go:build windows

package appserverdaemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// requestDaemonShutdown is injectable so the stop path can be pinned without a
// live managed app-server.
var requestDaemonShutdown = RequestDaemonShutdown

func daemonShutdownFilePath(pidFile string) string {
	return pidPathWithExtension(pidFile, "shutdown")
}

// requestGracefulPIDShutdown asks a pid-managed process to stop (Rust
// PidBackend::stop_with_grace). A managed app-server is asked over its
// protected control socket; the detached updater, which has no control socket,
// is asked through a plain-PID file beside its pid file. Unix signals the
// process instead (see pid_shutdown_unix.go).
func requestGracefulPIDShutdown(backend *PIDBackend, record *PIDRecord) error {
	if backend == nil || record == nil || record.PID == 0 {
		return nil
	}
	if backend.CommandKind == PIDCommandUpdateLoop {
		path := daemonShutdownFilePath(backend.PIDFile)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(strconv.FormatUint(uint64(record.PID), 10)), 0o600)
	}
	// A failed shutdown request is not fatal: the caller still terminates the
	// process once the grace period elapses (Rust logs and keeps waiting).
	socketPath := AppServerControlSocketPath(filepath.Dir(filepath.Dir(backend.PIDFile)))
	if err := requestDaemonShutdown(socketPath, record.PID, ControlSocketResponseTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "warning: managed app-server shutdown request failed; waiting for force deadline: %v\n", err)
	}
	return nil
}
