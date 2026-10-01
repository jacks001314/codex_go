package appserverdaemon

import (
	"context"
	"io"
	"os"
	"strconv"
	"time"
)

var daemonShutdownPollInterval = 200 * time.Millisecond

// takeDaemonShutdownRequest consumes the shutdown request addressed to pid.
//
// The file holds the plain decimal PID, matching Rust
// daemon_shutdown::take_shutdown_request: descendant app-servers may inherit
// the control path, so only the intended process consumes a request, and the
// read is bounded to a u32 PID plus one extra byte.
func takeDaemonShutdownRequest(path string, pid uint32) (bool, error) {
	if path == "" {
		return false, nil
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, 11))
	closeErr := file.Close()
	if readErr != nil {
		return false, readErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	if string(contents) != strconv.FormatUint(uint64(pid), 10) {
		return false, nil
	}
	// Synchronous consumption cannot lose a request to cancellation.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}

// WatchDaemonShutdownRequest polls the CODEX_DAEMON_SHUTDOWN_FILE for the
// updater's shutdown request and cancels ctx when its own PID arrives. The
// watcher is a no-op unless that env var is set, so only the detached updater
// (the process the daemon hands the file to) ever consumes a request.
func WatchDaemonShutdownRequest(ctx context.Context, cancel context.CancelFunc) {
	path := os.Getenv(DaemonShutdownFileEnv)
	if path == "" || cancel == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pid := uint32(os.Getpid())
	ticker := time.NewTicker(daemonShutdownPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			consumed, err := takeDaemonShutdownRequest(path, pid)
			if err != nil {
				// An unreadable control path must stop the updater rather than
				// disable shutdown (Rust update_loop::Signal::recv).
				cancel()
				return
			}
			if consumed {
				cancel()
				return
			}
		}
	}
}
