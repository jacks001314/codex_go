package appserverdaemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClaimManagedUpdaterPIDWritesOwnRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app-server-updater.pid")
	t.Setenv(UpdaterPIDFileEnv, path)
	if err := ClaimManagedUpdaterPID(); err != nil {
		t.Fatalf("ClaimManagedUpdaterPID error = %v", err)
	}
	record, err := ReadPIDRecord(path)
	if err != nil {
		t.Fatalf("ReadPIDRecord error = %v", err)
	}
	if record == nil || record.PID != uint32(os.Getpid()) || record.ProcessStartTime == "" {
		t.Fatalf("claimed record = %#v", record)
	}
}

func TestClaimManagedUpdaterPIDNoopWithoutEnv(t *testing.T) {
	t.Setenv(UpdaterPIDFileEnv, "")
	if err := ClaimManagedUpdaterPID(); err != nil {
		t.Fatalf("ClaimManagedUpdaterPID without env error = %v", err)
	}
}
