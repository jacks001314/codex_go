package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestApplyWithoutAuthMatchesRust pins Rust run_apply_command's credential
// check: the task fetch never runs when no ChatGPT auth resolved.
func TestApplyWithoutAuthMatchesRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("CODEX_API_KEY", "")
	t.Setenv("CODEX_ACCESS_TOKEN", "")
	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"apply", "task-abc"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil {
		t.Fatalf("apply returned nil error")
	}
	if err.Error() != "ChatGPT auth not available" {
		t.Fatalf("error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

// TestFeaturesEnableDisablePrintRustMessages pins the confirmation lines Rust
// prints after writing the feature flag.
func TestFeaturesEnableDisablePrintRustMessages(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"features", "enable", "web_search"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("features enable error = %v", err)
	}
	if got, want := stdout.String(), "Enabled feature `web_search` in config.toml.\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(context.Background(), []string{"features", "disable", "web_search"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("features disable error = %v", err)
	}
	if got, want := stdout.String(), "Disabled feature `web_search` in config.toml.\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

// TestLoginStatusLeavesNoLogFile pins Rust's behaviour: `login status` only
// reads the credential store and never creates the login tracing file.
func TestLoginStatusLeavesNoLogFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"login", "status"}, strings.NewReader(""), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("login status error = %v, want exit status 1", err)
	}
	if got, want := stderr.String(), "Not logged in\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, "log", "codex-login.log")); !os.IsNotExist(err) {
		t.Fatalf("codex-login.log created, stat error = %v", err)
	}
}
