package app

import (
	"os"
	"slices"
	"testing"

	"codex_go/cli"
	"codex_go/config"
)

// TestInteractiveEmbeddedReasoningOverridesLikeRust covers Rust #43921/#46533's
// embedded-thread defaults: summaries off unless configured, and concurrent
// summaries opt-in and disabled when summaries are off.
func TestInteractiveEmbeddedReasoningOverridesLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	got := interactiveEmbeddedReasoningOverrides(&cli.RootOptions{})
	if !slices.Contains(got, "model_reasoning_summary=none") {
		t.Fatalf("default overrides = %#v, want summaries disabled", got)
	}
	if !slices.Contains(got, "features.concurrent_reasoning_summaries=false") {
		t.Fatalf("default overrides = %#v, want concurrent summaries off", got)
	}

	writeConfig := func(body string) {
		t.Helper()
		if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}

	// Enabling concurrent summaries alone does not turn summaries on (#46533).
	writeConfig("[features]\nconcurrent_reasoning_summaries = true\n")
	got = interactiveEmbeddedReasoningOverrides(&cli.RootOptions{})
	if !slices.Contains(got, "model_reasoning_summary=none") || !slices.Contains(got, "features.concurrent_reasoning_summaries=false") {
		t.Fatalf("concurrent-only overrides = %#v, want summaries off", got)
	}

	// An explicit summary setting is preserved.
	writeConfig("model_reasoning_summary = \"detailed\"\n")
	got = interactiveEmbeddedReasoningOverrides(&cli.RootOptions{})
	if !slices.Contains(got, "model_reasoning_summary=detailed") {
		t.Fatalf("explicit summary overrides = %#v", got)
	}

	// A configured summary is honored, and an explicit concurrent opt-in stays on.
	writeConfig("model_reasoning_summary = \"concise\"\n[features]\nconcurrent_reasoning_summaries = true\n")
	got = interactiveEmbeddedReasoningOverrides(&cli.RootOptions{})
	if !slices.Contains(got, "model_reasoning_summary=concise") || !slices.Contains(got, "features.concurrent_reasoning_summaries=true") {
		t.Fatalf("configured overrides = %#v", got)
	}

	// Summaries off disable concurrent summaries even when opted in.
	writeConfig("model_reasoning_summary = \"none\"\n[features]\nconcurrent_reasoning_summaries = true\n")
	got = interactiveEmbeddedReasoningOverrides(&cli.RootOptions{})
	if !slices.Contains(got, "model_reasoning_summary=none") || !slices.Contains(got, "features.concurrent_reasoning_summaries=false") {
		t.Fatalf("summaries-off overrides = %#v", got)
	}
}
