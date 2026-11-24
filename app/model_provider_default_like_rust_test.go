package app

import (
	"os"
	"testing"

	"codex_go/cli"
	"codex_go/config"
	"codex_go/model"
)

// TestInteractiveDefaultModelProviderLikeRust pins the configured provider new
// threads use (Rust #63553 model_provider_id), which the picker compares against
// the active thread's provider.
func TestInteractiveDefaultModelProviderLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if got := interactiveDefaultModelProvider(&cli.RootOptions{}); got != model.OpenAIProviderID {
		t.Fatalf("built-in default provider = %q, want %s", got, model.OpenAIProviderID)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_provider = \"acme\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if got := interactiveDefaultModelProvider(&cli.RootOptions{}); got != "acme" {
		t.Fatalf("configured default provider = %q, want acme", got)
	}
}
