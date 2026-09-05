package appserverdaemon

import (
	"context"
	"encoding/json"
	"os"
	"time"
)

var daemonShutdownPollInterval = 200 * time.Millisecond

// WatchDaemonShutdownRequest polls the CODEX_DAEMON_SHUTDOWN_FILE for a
// shutdown request addressed to the current PID and cancels ctx when one
// arrives (Rust #42364). Requests for other PIDs and malformed files are
// ignored so one consumer can never consume another process's request.
func WatchDaemonShutdownRequest(ctx context.Context, cancel context.CancelFunc) {
	path := os.Getenv(DaemonShutdownFileEnv)
	if path == "" {
		return
	}
	ticker := time.NewTicker(daemonShutdownPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var request daemonShutdownRequest
			if json.Unmarshal(data, &request) != nil || request.PID == 0 {
				continue
			}
			if request.PID != uint32(os.Getpid()) {
				continue
			}
			_ = os.Remove(path)
			cancel()
			return
		}
	}
}
