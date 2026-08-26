package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteExternalTOMLRejectsRedirectedRepositoryTargetLikeRust(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(repo, ".gcode", "config.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeExternalTOML(target, map[string]any{"model": "gpt-5.1"}); err != nil {
		t.Fatalf("writeExternalTOML(plain) error = %v", err)
	}

	// Replace the generated config directory with a symlink pointing at the
	// codex home: the write must be rejected instead of following it.
	outside := filepath.Join(home, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, ".gcode")
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := writeExternalTOML(filepath.Join(link, "config.toml"), map[string]any{"model": "gpt-5.1"})
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("writeExternalTOML(redirected) error = %v", err)
	}
}
