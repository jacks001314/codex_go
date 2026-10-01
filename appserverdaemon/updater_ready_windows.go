//go:build windows

package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Windows updater publication handshake: the launcher holds the operation and
// reservation locks until its successor initializes and acknowledges readiness
// (Rust backend/pid_windows.rs).

var (
	// updaterStartTimeout bounds the readiness handshake (Rust START_TIMEOUT).
	updaterStartTimeout = PIDStartTimeout
	// updaterReadyPollInterval is the handshake cadence (Rust STOP_POLL_INTERVAL).
	updaterReadyPollInterval = PIDStopPollInterval
	// terminateUpdaterProcess is injectable so the failure path can be pinned
	// without killing a live process.
	terminateUpdaterProcess = terminatePIDProcess
)

// updaterReadyFilePath is the acknowledgment marker the updater writes once it
// serves its request socket.
func updaterReadyFilePath(pidFile string) string {
	return pidPathWithExtension(pidFile, "ready")
}

// clearUpdaterReadyMarker drops a stale readiness marker before a launch
// (Rust backend::pid_start).
func clearUpdaterReadyMarker(pidFile string) {
	if strings.TrimSpace(pidFile) == "" {
		return
	}
	_ = os.Remove(updaterReadyFilePath(pidFile))
}

// WaitForUpdaterOwnership waits until this updater's launcher published its pid
// record, so the updater never claims a file another launch owns (Rust
// PidBackend::wait_for_ownership).
func WaitForUpdaterOwnership(backend *PIDBackend) error {
	if backend == nil || backend.CommandKind != PIDCommandUpdateLoop || strings.TrimSpace(backend.PIDFile) == "" {
		return nil
	}
	self := uint32(os.Getpid())
	deadline := time.Now().Add(updaterStartTimeout)
	for {
		state, err := ReadPIDFileState(backend.PIDFile)
		if err != nil {
			return err
		}
		if state.Kind == PIDFileRunning && state.Record != nil && state.Record.PID == self {
			active, err := processMatchesPIDRecord(state.Record)
			if err != nil {
				return err
			}
			if active {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("updater was not published by its launcher")
		}
		time.Sleep(updaterReadyPollInterval)
	}
}

// MarkUpdaterReady acknowledges a successfully initialized updater (Rust
// PidBackend::mark_ready).
func MarkUpdaterReady(backend *PIDBackend) error {
	if backend == nil || strings.TrimSpace(backend.PIDFile) == "" {
		return nil
	}
	if err := os.WriteFile(updaterReadyFilePath(backend.PIDFile), nil, 0o600); err != nil {
		return fmt.Errorf("failed to acknowledge updater startup: %w", err)
	}
	return nil
}

// finishUpdaterStart waits for the detached updater's readiness acknowledgment
// and, on failure, terminates it and clears its pid record (Rust
// PidBackend::finish_updater_start). Only the updater backend runs it.
func finishUpdaterStart(backend *PIDBackend, record *PIDRecord) error {
	if backend == nil || backend.CommandKind != PIDCommandUpdateLoop || record == nil || record.PID == 0 {
		return nil
	}
	ready := updaterReadyFilePath(backend.PIDFile)
	deadline := time.Now().Add(updaterStartTimeout)
	startupErr := func() error {
		for {
			active, err := processMatchesPIDRecord(record)
			if err != nil {
				return err
			}
			if !active {
				return errors.New("updater exited before becoming ready")
			}
			if err := os.Remove(ready); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return err
			}
			if time.Now().After(deadline) {
				return errors.New("updater did not become ready")
			}
			time.Sleep(updaterReadyPollInterval)
		}
	}()
	if startupErr == nil {
		return nil
	}
	if active, err := processMatchesPIDRecord(record); err == nil && active {
		_ = terminateUpdaterProcess(record.PID)
		stopDeadline := time.Now().Add(updaterStartTimeout)
		for {
			stillActive, err := processMatchesPIDRecord(record)
			if err != nil || !stillActive {
				break
			}
			if time.Now().After(stopDeadline) {
				return errors.New("failed updater did not exit; ownership was not restored")
			}
			time.Sleep(updaterReadyPollInterval)
		}
	}
	_ = os.Remove(backend.PIDFile)
	return startupErr
}
