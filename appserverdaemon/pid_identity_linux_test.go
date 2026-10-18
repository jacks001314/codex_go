//go:build linux

package appserverdaemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLinuxProcStatUsesFinalCommParenthesis(t *testing.T) {
	fields := []string{"S"}
	for index := 1; index < 19; index++ {
		fields = append(fields, "0")
	}
	fields = append(fields, "424242", "0")
	stat := "123 (worker (nested) name) " + strings.Join(fields, " ")
	state, ticks, err := parseLinuxProcStat([]byte(stat))
	if err != nil {
		t.Fatalf("parseLinuxProcStat() error = %v", err)
	}
	if state != "S" || ticks != 424242 {
		t.Fatalf("parseLinuxProcStat() = %q, %d", state, ticks)
	}
}

func TestParseLinuxProcStatRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{"", "123 no comm", "123 (short) S 1"} {
		if _, _, err := parseLinuxProcStat([]byte(input)); err == nil {
			t.Fatalf("parseLinuxProcStat(%q) succeeded", input)
		}
	}
}

func TestPromoteLegacyIdentityRecordsNativeIdentity(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "app-server.pid")
	pid := uint32(os.Getpid())
	startTime, err := readPIDProcessStartTime(pid)
	if err != nil {
		t.Fatalf("readPIDProcessStartTime() error = %v", err)
	}
	if err := WritePIDRecord(pidFile, &PIDRecord{PID: pid, ProcessStartTime: startTime}); err != nil {
		t.Fatalf("WritePIDRecord() error = %v", err)
	}
	backend := &PIDBackend{PIDFile: pidFile}
	if err := backend.PromoteLegacyIdentity(); err != nil {
		t.Fatalf("PromoteLegacyIdentity() error = %v", err)
	}
	record, err := ReadPIDRecord(pidFile)
	if err != nil {
		t.Fatalf("ReadPIDRecord() error = %v", err)
	}
	if record == nil || record.ProcessIdentity == nil || record.ProcessIdentity.StartTicks == nil || record.ProcessIdentity.BootID == "" {
		t.Fatalf("promoted record = %#v", record)
	}
	active, err := processMatchesPIDRecord(record)
	if err != nil || !active {
		t.Fatalf("native record match = %v, %v", active, err)
	}
}

func TestNativeIdentityRejectsDifferentBoot(t *testing.T) {
	bootID, err := readLinuxBootID()
	if err != nil {
		t.Fatalf("readLinuxBootID() error = %v", err)
	}
	identity := &ProcessIdentity{BootID: bootID + "-old", StartTicks: uint64Pointer(1)}
	matched, _, available, err := matchNativePIDProcessIdentity(identity, uint32(os.Getpid()))
	if err != nil {
		t.Fatalf("matchNativePIDProcessIdentity() error = %v", err)
	}
	if !available || matched {
		t.Fatalf("different boot matched: available=%v matched=%v", available, matched)
	}
}
