//go:build !windows

package appserverdaemon

// requestGracefulPIDShutdown asks the pid-managed process to shut down. Unix
// processes receive SIGTERM; Windows writes a shutdown-file request that the
// app-server watches (see pid_shutdown_windows.go).
func requestGracefulPIDShutdown(_ *PIDBackend, record *PIDRecord) error {
	if record == nil {
		return nil
	}
	return terminatePIDProcess(record.PID)
}
