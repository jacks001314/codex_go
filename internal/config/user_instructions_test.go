package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserInstructionsMissingFiles(t *testing.T) {
	loaded := NewUserInstructionsProvider(t.TempDir()).Load()
	if loaded.Instructions != nil || len(loaded.Warnings) != 0 {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestUserInstructionsOverridePrecedence(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, DefaultAgentsMDFilename), []byte("default"), 0o600); err != nil {
		t.Fatalf("write default: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, LocalAgentsMDFilename), []byte("override"), 0o600); err != nil {
		t.Fatalf("write override: %v", err)
	}
	loaded := NewUserInstructionsProvider(home).Load()
	if loaded.Instructions == nil || loaded.Instructions.Text != "override" {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestUserInstructionsEmptyOverrideFallsBack(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, LocalAgentsMDFilename), []byte(" \n\t"), 0o600); err != nil {
		t.Fatalf("write override: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, DefaultAgentsMDFilename), []byte("\n default \n"), 0o600); err != nil {
		t.Fatalf("write default: %v", err)
	}
	loaded := NewUserInstructionsProvider(home).Load()
	if loaded.Instructions == nil || loaded.Instructions.Text != "default" {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestUserInstructionsDirectoryOverrideFallsBack(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, LocalAgentsMDFilename), 0o700); err != nil {
		t.Fatalf("mkdir override: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, DefaultAgentsMDFilename), []byte("default"), 0o600); err != nil {
		t.Fatalf("write default: %v", err)
	}
	loaded := NewUserInstructionsProvider(home).Load()
	if loaded.Instructions == nil || loaded.Instructions.Text != "default" {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestUserInstructionsInvalidUTF8IsLossy(t *testing.T) {
	home := t.TempDir()
	data := append([]byte("global"), 0xff)
	data = append(data, []byte(" doc")...)
	if err := os.WriteFile(filepath.Join(home, DefaultAgentsMDFilename), data, 0o600); err != nil {
		t.Fatalf("write default: %v", err)
	}
	loaded := NewUserInstructionsProvider(home).Load()
	if loaded.Instructions == nil || loaded.Instructions.Text != "global\ufffd doc" {
		t.Fatalf("loaded = %#v", loaded)
	}
}
