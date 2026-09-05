//go:build windows

package appserverdaemon

import (
	"os"
	"testing"
)

func TestWindowsPIDProcessExistsAndCreationTime(t *testing.T) {
	pid := uint32(os.Getpid())
	if !pidProcessExists(pid) {
		t.Fatalf("current process %d should exist", pid)
	}
	first, err := readPIDProcessStartTime(pid)
	if err != nil {
		t.Fatalf("readPIDProcessStartTime(%d) error = %v", pid, err)
	}
	if first == "" {
		t.Fatalf("current process %d creation time is empty", pid)
	}
	second, err := readPIDProcessStartTime(pid)
	if err != nil {
		t.Fatalf("second readPIDProcessStartTime(%d) error = %v", pid, err)
	}
	if first != second {
		t.Fatalf("creation time changed for live process: %q vs %q", first, second)
	}
	if pidProcessExists(0xFFFFFFFF) {
		t.Fatalf("unlikely pid %d reported as existing", 0xFFFFFFFF)
	}
}

func TestWindowsProcessMatchesPIDRecordRejectsReusedPID(t *testing.T) {
	pid := uint32(os.Getpid())
	if matched, err := processMatchesPIDRecord(&PIDRecord{PID: pid, ProcessStartTime: "1970-01-01T00:00:00.000000000Z"}); err != nil || matched {
		t.Fatalf("processMatchesPIDRecord with stale start = %v, %v; want false, nil", matched, err)
	}
	if matched, err := processMatchesPIDRecord(&PIDRecord{PID: 0}); err != nil || matched {
		t.Fatalf("processMatchesPIDRecord nil record = %v, %v; want false, nil", matched, err)
	}
	start, err := readPIDProcessStartTime(pid)
	if err != nil {
		t.Fatalf("readPIDProcessStartTime(%d) error = %v", pid, err)
	}
	if matched, err := processMatchesPIDRecord(&PIDRecord{PID: pid, ProcessStartTime: start}); err != nil || !matched {
		t.Fatalf("processMatchesPIDRecord matching record = %v, %v; want true, nil", matched, err)
	}
}

func TestWindowsTerminateMissingProcessIsNoop(t *testing.T) {
	if err := terminatePIDProcess(0xFFFFFFFE); err != nil {
		t.Fatalf("terminatePIDProcess on missing pid error = %v", err)
	}
	if err := forceTerminatePIDProcess(0xFFFFFFFE, true); err != nil {
		t.Fatalf("forceTerminatePIDProcess on missing pid error = %v", err)
	}
}
