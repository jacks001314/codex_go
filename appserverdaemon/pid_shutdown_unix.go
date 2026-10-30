//go:build !windows

package appserverdaemon

// requestGracefulPIDShutdown asks the pid-managed process to shut down. Unix
// processes receive SIGTERM; Windows writes a shutdown-file request that the
// app-server watches (see pid_shutdown_windows.go).
func requestGracefulPIDShutdown(backend *PIDBackend, record *PIDRecord) error {
	if record == nil {
		return nil
	}
	// The updater may have an installer child in its process group; signal the
	// group so stopping the updater stops the installer too (Rust #43542).
	if backend != nil && backend.CommandKind == PIDCommandUpdateLoop {
		return terminatePIDProcessGroup(record.PID)
	}
	return terminatePIDProcess(record.PID)
}
