package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Mirrors Rust #50437's cli/tests/sandbox_uninstall.rs: help and invalid
// arguments must work without loading configuration and must leave the user's
// configuration and credentials untouched.
func TestSandboxUninstallHelpAndInvalidArgumentsPreserveUserData(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	authPath := filepath.Join(home, "auth.json")
	if err := os.WriteFile(configPath, []byte("invalid config ["), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(authPath, []byte("preserve credentials"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	t.Setenv("CODEX_HOME", home)

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"sandbox", "uninstall", "--help"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("sandbox uninstall --help error = %v", err)
	}
	if !strings.Contains(stdout.String(), "codex sandbox uninstall") {
		t.Fatalf("help output = %q", stdout.String())
	}

	err := Run(context.Background(), []string{"sandbox", "uninstall", "unexpected"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("sandbox uninstall unexpected error = %v", err)
	}

	assertFileContents(t, configPath, "invalid config [")
	assertFileContents(t, authPath, "preserve credentials")
}

func assertFileContents(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, string(data), want)
	}
}
