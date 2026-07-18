package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveGeneratesAndPersistsID(t *testing.T) {
	home := t.TempDir()
	id, err := ResolveInstallationID(home)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if normalizeUUID(id) == "" {
		t.Fatalf("id = %q, want UUID", id)
	}
	data, err := os.ReadFile(filepath.Join(home, InstallationIDFilename))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != id {
		t.Fatalf("persisted = %q, want %q", data, id)
	}
}

func TestResolveReusesExistingUppercaseID(t *testing.T) {
	home := t.TempDir()
	existing := "A0B1C2D3-E4F5-4678-9234-ABCDEF123456"
	if err := os.WriteFile(filepath.Join(home, InstallationIDFilename), []byte(existing), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	id, err := ResolveInstallationID(home)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if id != "a0b1c2d3-e4f5-4678-9234-abcdef123456" {
		t.Fatalf("id = %q", id)
	}
}

func TestResolveRewritesInvalidID(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, InstallationIDFilename), []byte("not-a-uuid"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	id, err := ResolveInstallationID(home)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if id == "not-a-uuid" || normalizeUUID(id) == "" {
		t.Fatalf("id = %q, want generated UUID", id)
	}
}
