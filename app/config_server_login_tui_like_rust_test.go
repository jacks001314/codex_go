package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"codex_go/cli"
	"codex_go/config"
)

// Rust #57175: an interactive launch that requires a customer-hosted
// configuration server signs in before the TUI initializes, so the first
// thread bootstrap sees the managed bundle.
func TestInteractiveStartupSignsIntoRequiredConfigServerLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("GCODE_HOME", "")
	requirements := `
[config_server]
base_url = "https://config.example.com/settings"

[config_server.oauth]
authorization_url = "https://config.example.com/authorize"
token_url = "https://config.example.com/token"
client_id = "company-client"
`
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}

	buffer := &bytes.Buffer{}
	var (
		called       bool
		gotHome      string
		gotServerURL string
	)
	restore := interactiveStartupConfigServerLogin
	interactiveStartupConfigServerLogin = func(ctx context.Context, codexHome string, reqs *config.ConfigRequirements, onURL func(string)) error {
		called = true
		gotHome = codexHome
		if reqs != nil && reqs.ConfigServer != nil {
			gotServerURL = reqs.ConfigServer.BaseURL
		}
		onURL("https://config.example.com/authorize?login=1")
		return nil
	}
	defer func() { interactiveStartupConfigServerLogin = restore }()

	if err := ensureInteractiveStartupConfigServerLogin(context.Background(), &cli.RootOptions{}, buffer); err != nil {
		t.Fatalf("ensureInteractiveStartupConfigServerLogin: %v", err)
	}
	if !called {
		t.Fatal("a required config server must trigger sign-in before the TUI starts")
	}
	if gotHome != home {
		t.Fatalf("codex home = %q, want %q", gotHome, home)
	}
	if gotServerURL != "https://config.example.com/settings" {
		t.Fatalf("config server = %q", gotServerURL)
	}
	if printed := buffer.String(); !bytes.Contains([]byte(printed), []byte("https://config.example.com/authorize?login=1")) {
		t.Fatalf("sign-in URL was not printed to stderr: %q", printed)
	}
}

func TestInteractiveStartupSkipsLoginWithoutRequiredServerLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("GCODE_HOME", "")

	called := false
	restore := interactiveStartupConfigServerLogin
	interactiveStartupConfigServerLogin = func(context.Context, string, *config.ConfigRequirements, func(string)) error {
		called = true
		return nil
	}
	defer func() { interactiveStartupConfigServerLogin = restore }()

	if err := ensureInteractiveStartupConfigServerLogin(context.Background(), &cli.RootOptions{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("ensureInteractiveStartupConfigServerLogin: %v", err)
	}
	if called {
		t.Fatal("a launch without a required config server must not sign in")
	}
}
