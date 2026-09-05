package appserverdaemon

import (
	"fmt"
	"os"
)

// ClaimManagedUpdaterPID writes the current process's PID record to the update
// PID file named by CODEX_UPDATER_PID_FILE. Reexecuted successor updaters call
// this before running the loop so ownership moves to the changed managed
// executable (Rust #42392). Without the env var this is a no-op (normal
// lifecycle launches already write the record from PIDBackend.Start).
func ClaimManagedUpdaterPID() error {
	path := os.Getenv(UpdaterPIDFileEnv)
	if path == "" {
		return nil
	}
	start, err := readPIDProcessStartTime(uint32(os.Getpid()))
	if err != nil {
		return fmt.Errorf("failed to record updater process %d startup: %w", os.Getpid(), err)
	}
	record := &PIDRecord{PID: uint32(os.Getpid()), ProcessStartTime: start}
	if err := WritePIDRecord(path, record); err != nil {
		return fmt.Errorf("failed to claim updater pid file %s: %w", path, err)
	}
	return nil
}
