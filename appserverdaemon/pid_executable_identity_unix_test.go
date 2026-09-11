//go:build !windows

package appserverdaemon

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/install"
)

// Mirrors Rust #43552: the PID record stores the launch-time executable
// identity, resolved through the selected symlink, and exposes it only while
// the process is active.
func TestPIDRecordRecordsLaunchedExecutableIdentityLikeRust(t *testing.T) {
	scriptDir := t.TempDir()
	binary := filepath.Join(scriptDir, "codex")
	contents := []byte("#!/bin/sh\ntrap 'exit 0' TERM\nwhile true; do sleep 1; done\n")
	if err := os.WriteFile(binary, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "codex-link")
	if err := os.Symlink(binary, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	backend := NewPIDBackend(BackendPaths{CodexBin: link, PIDFile: filepath.Join(t.TempDir(), PIDFileName)})
	pid, err := backend.Start()
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if pid == nil {
		t.Fatal("Start returned a nil pid")
	}
	t.Cleanup(func() { _ = backend.Stop() })

	record, err := ReadPIDRecord(backend.PIDFile)
	if err != nil {
		t.Fatalf("ReadPIDRecord error = %v", err)
	}
	want := install.ExecutableIdentityFromBytes(contents)
	if record.ExecutableIdentity == nil || *record.ExecutableIdentity != want {
		t.Fatalf("executable identity = %#v, want %#v", record.ExecutableIdentity, want)
	}

	// Retargeting the selected symlink must not change the recorded identity of
	// the already launched binary.
	otherBinary := filepath.Join(scriptDir, "other")
	otherContents := []byte("#!/bin/sh\ntrap 'exit 0' TERM\nwhile true; do sleep 2; done\n")
	if err := os.WriteFile(otherBinary, otherContents, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(otherBinary, link); err != nil {
		t.Fatal(err)
	}
	record, err = ReadPIDRecord(backend.PIDFile)
	if err != nil {
		t.Fatalf("ReadPIDRecord error = %v", err)
	}
	if record.ExecutableIdentity == nil || *record.ExecutableIdentity != want {
		t.Fatalf("identity after retarget = %#v, want %#v", record.ExecutableIdentity, want)
	}
	if retargeted := install.ExecutableIdentityFromBytes(otherContents); *record.ExecutableIdentity == retargeted {
		t.Fatal("recorded identity followed the retargeted symlink")
	}

	identity, err := backend.RunningExecutableIdentity()
	if err != nil || identity == nil || *identity != want {
		t.Fatalf("RunningExecutableIdentity() = %#v, %v; want %#v", identity, err, want)
	}
	if err := backend.Stop(); err != nil {
		t.Fatalf("Stop error = %v", err)
	}
	if identity, err := backend.RunningExecutableIdentity(); err != nil || identity != nil {
		t.Fatalf("identity after stop = %#v, %v; want nil", identity, err)
	}
}

func TestPIDRecordAcceptsLegacyRecordWithoutExecutableIdentityLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), PIDFileName)
	if err := os.WriteFile(path, []byte("{\"pid\":123,\"processStartTime\":\"legacy\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := ReadPIDRecord(path)
	if err != nil {
		t.Fatalf("ReadPIDRecord error = %v", err)
	}
	if record.ExecutableIdentity != nil {
		t.Fatalf("legacy record identity = %#v, want nil", record.ExecutableIdentity)
	}
}
