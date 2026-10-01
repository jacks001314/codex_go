//go:build windows

package appserverdaemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testUpdaterBackend builds an updater backend over a temp pid file.
func testUpdaterBackend(t *testing.T) *PIDBackend {
	t.Helper()
	return NewPIDUpdateLoopBackend(BackendPaths{
		CodexBin:      os.Args[0],
		UpdatePIDFile: filepath.Join(t.TempDir(), "daemon-updater.pid"),
	})
}

// shortenUpdaterHandshake keeps the readiness handshake tests fast.
func shortenUpdaterHandshake(t *testing.T) {
	t.Helper()
	originalTimeout := updaterStartTimeout
	originalInterval := updaterReadyPollInterval
	updaterStartTimeout = 300 * time.Millisecond
	updaterReadyPollInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		updaterStartTimeout = originalTimeout
		updaterReadyPollInterval = originalInterval
	})
}

// spawnSleepingChild starts a real child process this test can terminate, which
// is what the readiness handshake's failure path acts on.
func spawnSleepingChild(t *testing.T) *PIDRecord {
	t.Helper()
	command := exec.Command("cmd.exe", "/c", "ping -n 30 127.0.0.1 >NUL")
	if err := command.Start(); err != nil {
		t.Skipf("cannot spawn a supervised child process: %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})
	start, err := readPIDProcessStartTime(uint32(command.Process.Pid))
	if err != nil {
		t.Fatalf("readPIDProcessStartTime error = %v", err)
	}
	return &PIDRecord{PID: uint32(command.Process.Pid), ProcessStartTime: start}
}

// TestUpdaterReadinessHandshakeLikeRust pins the Windows publication handshake:
// a launcher waits for the updater's marker and consumes it, while an updater
// that never acknowledges is terminated and loses its pid record.
func TestUpdaterReadinessHandshakeLikeRust(t *testing.T) {
	shortenUpdaterHandshake(t)

	// An updater that never acknowledges is terminated and unrecorded.
	backend := testUpdaterBackend(t)
	silent := spawnSleepingChild(t)
	if err := WritePIDRecord(backend.PIDFile, silent); err != nil {
		t.Fatalf("WritePIDRecord error = %v", err)
	}
	err := finishUpdaterStart(backend, silent)
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("finishUpdaterStart(silent) error = %v", err)
	}
	if active, matchErr := processMatchesPIDRecord(silent); matchErr != nil || active {
		t.Fatalf("the silent updater was not terminated: active=%v err=%v", active, matchErr)
	}
	if _, statErr := os.Stat(backend.PIDFile); !os.IsNotExist(statErr) {
		t.Fatal("a failed updater start must clear its pid record")
	}

	// An acknowledging updater keeps its record and loses only the marker.
	backend = testUpdaterBackend(t)
	ready := spawnSleepingChild(t)
	if err := WritePIDRecord(backend.PIDFile, ready); err != nil {
		t.Fatalf("WritePIDRecord error = %v", err)
	}
	if err := MarkUpdaterReady(backend); err != nil {
		t.Fatalf("MarkUpdaterReady error = %v", err)
	}
	if err := finishUpdaterStart(backend, ready); err != nil {
		t.Fatalf("finishUpdaterStart(acknowledged) error = %v", err)
	}
	if _, statErr := os.Stat(updaterReadyFilePath(backend.PIDFile)); !os.IsNotExist(statErr) {
		t.Fatal("the readiness marker was not consumed")
	}
	if _, statErr := os.Stat(backend.PIDFile); statErr != nil {
		t.Fatalf("the acknowledged updater lost its pid record: %v", statErr)
	}

	// An updater that already exited is reported before the deadline.
	dead := &PIDRecord{PID: 0xFFFFFFFE, ProcessStartTime: "gone"}
	if err := finishUpdaterStart(backend, dead); err == nil ||
		!strings.Contains(err.Error(), "exited before becoming ready") {
		t.Fatalf("finishUpdaterStart(dead) error = %v", err)
	}
}

// TestWaitForUpdaterOwnershipLikeRust pins that the updater waits for its
// launcher to publish the pid record before it acknowledges readiness.
func TestWaitForUpdaterOwnershipLikeRust(t *testing.T) {
	shortenUpdaterHandshake(t)
	backend := testUpdaterBackend(t)

	if err := WaitForUpdaterOwnership(backend); err == nil ||
		!strings.Contains(err.Error(), "was not published by its launcher") {
		t.Fatalf("WaitForUpdaterOwnership(unpublished) error = %v", err)
	}

	start, err := readPIDProcessStartTime(uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("readPIDProcessStartTime error = %v", err)
	}
	self := &PIDRecord{PID: uint32(os.Getpid()), ProcessStartTime: start}
	if err := WritePIDRecord(backend.PIDFile, self); err != nil {
		t.Fatalf("WritePIDRecord error = %v", err)
	}
	if err := WaitForUpdaterOwnership(backend); err != nil {
		t.Fatalf("WaitForUpdaterOwnership(published) error = %v", err)
	}

	// A published record for another process does not count as ownership.
	if err := WritePIDRecord(backend.PIDFile, &PIDRecord{PID: 0xFFFFFFFE, ProcessStartTime: "other"}); err != nil {
		t.Fatalf("WritePIDRecord(other) error = %v", err)
	}
	if err := WaitForUpdaterOwnership(backend); err == nil {
		t.Fatal("WaitForUpdaterOwnership accepted another process's record")
	}
}

// TestClearUpdaterReadyMarkerLikeRust pins that a stale acknowledgment is
// dropped before a launch.
func TestClearUpdaterReadyMarkerLikeRust(t *testing.T) {
	backend := testUpdaterBackend(t)
	if err := MarkUpdaterReady(backend); err != nil {
		t.Fatalf("MarkUpdaterReady error = %v", err)
	}
	clearUpdaterReadyMarker(backend.PIDFile)
	if _, err := os.Stat(updaterReadyFilePath(backend.PIDFile)); !os.IsNotExist(err) {
		t.Fatal("clearUpdaterReadyMarker left the marker behind")
	}
}
