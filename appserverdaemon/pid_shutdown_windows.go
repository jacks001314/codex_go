//go:build windows

package appserverdaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// daemonShutdownRequest is the JSON payload the pid-managed app server watches
// in its CODEX_DAEMON_SHUTDOWN_FILE (Rust #42364).
type daemonShutdownRequest struct {
	PID uint32 `json:"pid"`
}

func daemonShutdownFilePath(pidFile string) string {
	return pidPathWithExtension(pidFile, "shutdown")
}

func requestGracefulPIDShutdown(backend *PIDBackend, record *PIDRecord) error {
	if backend == nil || record == nil || record.PID == 0 {
		return nil
	}
	path := daemonShutdownFilePath(backend.PIDFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(daemonShutdownRequest{PID: record.PID})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
