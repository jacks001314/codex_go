package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindCodexHomeFromEnvMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-codex-home")
	_, err := FindCodexHomeFromEnv(missing)
	if err == nil || !strings.Contains(err.Error(), "CODEX_HOME") {
		t.Fatalf("missing env error = %v", err)
	}
}

func TestFindCodexHomeFromEnvFilePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex-home.txt")
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := FindCodexHomeFromEnv(path)
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file env error = %v", err)
	}
}

func TestFindCodexHomeFromEnvValidDirectory(t *testing.T) {
	dir := t.TempDir()
	got, err := FindCodexHomeFromEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != expected {
		t.Fatalf("home = %q want %q", got, expected)
	}
}

func TestFindCodexHomeFromEnvDefault(t *testing.T) {
	got, err := FindCodexHomeFromEnv("")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != ".gcode" {
		t.Fatalf("default home = %q", got)
	}
}
