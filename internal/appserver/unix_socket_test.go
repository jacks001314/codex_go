package appserver

import (
	"path/filepath"
	"testing"
)

func TestUnixSocketPathDefaultAndExplicit(t *testing.T) {
	home := filepath.Join("tmp", "codex-home")
	defaultPath, err := UnixSocketPath("unix://", home)
	if err != nil {
		t.Fatalf("UnixSocketPath default error = %v", err)
	}
	if defaultPath != AppServerControlSocketPath(home) {
		t.Fatalf("default path = %q, want %q", defaultPath, AppServerControlSocketPath(home))
	}

	explicit, err := UnixSocketPath("unix://tmp/codex.sock", home)
	if err != nil {
		t.Fatalf("UnixSocketPath explicit error = %v", err)
	}
	wantExplicit, err := filepath.Abs(filepath.Join("tmp", "codex.sock"))
	if err != nil {
		t.Fatalf("Abs error = %v", err)
	}
	if explicit != filepath.Clean(wantExplicit) {
		t.Fatalf("explicit path = %q", explicit)
	}
}

func TestUnixSocketPathRejectsUnsupportedScheme(t *testing.T) {
	_, err := UnixSocketPath("tcp://127.0.0.1:0", t.TempDir())
	if err == nil {
		t.Fatal("UnixSocketPath returned nil error, want unsupported scheme")
	}
}
