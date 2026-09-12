package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemovedThreadStoreEndpointFailsFastLikeRust mirrors Rust's
// load_config guard: the removed remote thread-store endpoint must fail the
// load instead of silently falling back to local persistence.
func TestRemovedThreadStoreEndpointFailsFastLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(
		ConfigPath(home),
		[]byte("experimental_thread_store_endpoint = \"https://example.com\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	_, err := LoadEffectiveWithOptions(home, nil)
	if err == nil {
		t.Fatal("LoadEffectiveWithOptions() error = nil, want the removed-key rejection")
	}
	if !strings.Contains(err.Error(), "`experimental_thread_store_endpoint` is no longer supported; remove it from config.toml") {
		t.Fatalf("error = %v, want Rust's message", err)
	}
}

// TestExperimentalCompactPromptFileIsAcceptedLikeRust pins that the supported
// experimental compact prompt file key is no longer rejected as unknown.
func TestExperimentalCompactPromptFileIsAcceptedLikeRust(t *testing.T) {
	home := t.TempDir()
	promptPath := filepath.Join(t.TempDir(), "compact.md")
	body := "experimental_compact_prompt_file = " + quoteTOMLString(promptPath) + "\n"
	if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	cfg, err := LoadWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	if got, _ := cfg.Values["experimental_compact_prompt_file"].(string); got != promptPath {
		t.Fatalf("experimental_compact_prompt_file = %q, want %q", got, promptPath)
	}
}

// quoteTOMLString renders a basic TOML string, escaping backslashes so Windows
// paths survive parsing.
func quoteTOMLString(value string) string {
	replaced := strings.ReplaceAll(value, `\`, `\\`)
	replaced = strings.ReplaceAll(replaced, `"`, `\"`)
	return `"` + replaced + `"`
}
