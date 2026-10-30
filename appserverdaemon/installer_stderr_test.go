package appserverdaemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestInstallerStderrTailFitsTheManualUpdateResponseLimitLikeRust pins the
// budget behind INSTALLER_STDERR_TAIL_BYTES (Rust #50499): even when every
// captured byte needs the six-byte `\u00XX` JSON escape, the encoded error
// response still fits the 16 KiB the client reads.
func TestInstallerStderrTailFitsTheManualUpdateResponseLimitLikeRust(t *testing.T) {
	tail := &installerStderrTail{}
	payload := bytes.Repeat([]byte{0x01}, installerStderrTailBytes)
	if _, err := tail.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	message := installerExitError(errors.New("exit status 1"), tail).Error()
	encoded, err := json.Marshal(map[string]any{"Err": message})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if len(encoded) > manualUpdateResponseLimit {
		t.Fatalf("escaped response = %d bytes, want <= %d", len(encoded), manualUpdateResponseLimit)
	}
	var outcome struct {
		Err *string `json:"Err"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(&outcome); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if outcome.Err == nil || !strings.Contains(*outcome.Err, "standalone Codex updater exited with error: exit status 1") {
		t.Fatalf("decoded error = %v", outcome.Err)
	}
}

// TestInstallerStderrTailKeepsNewestWindowLikeRust covers the bounded capture
// (Rust update_loop::INSTALLER_STDERR_TAIL_BYTES): a single oversized chunk
// keeps only its final window.
func TestInstallerStderrTailKeepsNewestWindowLikeRust(t *testing.T) {
	tail := &installerStderrTail{}
	if _, err := tail.Write([]byte(strings.Repeat("x", installerStderrTailBytes+10) + "boom")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got := tail.String()
	if len(got) != installerStderrTailBytes {
		t.Fatalf("tail length = %d, want %d", len(got), installerStderrTailBytes)
	}
	if !strings.HasSuffix(got, "boom") {
		t.Fatalf("tail lost the newest bytes: %q", got[len(got)-8:])
	}
	if strings.Contains(got, "x") == false {
		t.Fatalf("tail lost the retained window: %q", got[:8])
	}
}

// TestInstallerStderrTailRollsAcrossWritesLikeRust covers the incremental case:
// each write drops just enough of the oldest bytes to make room.
func TestInstallerStderrTailRollsAcrossWritesLikeRust(t *testing.T) {
	tail := &installerStderrTail{}
	if _, err := tail.Write([]byte(strings.Repeat("a", installerStderrTailBytes))); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := tail.Write([]byte("b")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, err := tail.Write([]byte("ccccc")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got := tail.String()
	if len(got) != installerStderrTailBytes {
		t.Fatalf("tail length = %d, want %d", len(got), installerStderrTailBytes)
	}
	if !strings.HasSuffix(got, "bccccc") {
		t.Fatalf("tail = ...%q, want suffix bccccc", got[len(got)-8:])
	}
	if strings.Contains(strings.TrimSuffix(got, "bccccc"), "b") {
		t.Fatalf("tail retained more than the newest window")
	}
}

// TestInstallerExitErrorFormatsTailLikeRust pins the failure message: the tail
// is trimmed and appended on its own line, and an empty capture leaves the bare
// status message.
func TestInstallerExitErrorFormatsTailLikeRust(t *testing.T) {
	base := errors.New("exit status 1")
	if got := installerExitError(base, &installerStderrTail{}).Error(); got != "standalone Codex updater exited with error: exit status 1" {
		t.Fatalf("empty-tail message = %q", got)
	}
	tail := &installerStderrTail{}
	if _, err := tail.Write([]byte("  boom\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	want := "standalone Codex updater exited with error: exit status 1:\nboom"
	if got := installerExitError(base, tail).Error(); got != want {
		t.Fatalf("tail message = %q, want %q", got, want)
	}
	if !errors.Is(installerExitError(base, tail), base) {
		t.Fatalf("installerExitError no longer wraps the process error")
	}
}
