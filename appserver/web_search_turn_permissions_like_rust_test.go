package appserver

import (
	"testing"

	"codex_go/codexapi"
	"codex_go/config"
)

// Rust #58189: standalone web search derives availability and
// external_web_access from the turn-scoped web search mode. A settings update
// made during a turn therefore only affects the next turn's snapshot.
func TestStandaloneWebSearchUsesTurnScopedModeLikeRust(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"web_search": "cached"}}
	turnMode := webSearchModeFromConfig(cfg)
	turnSettings := webSearchSettingsFromConfig(cfg, turnMode)
	if turnMode != codexapi.WebSearchModeCached {
		t.Fatalf("turn mode = %q", turnMode)
	}
	if turnSettings.ExternalWebAccess == nil || turnSettings.ExternalWebAccess.Boolean == nil || *turnSettings.ExternalWebAccess.Boolean {
		t.Fatalf("cached-turn external_web_access = %#v", turnSettings.ExternalWebAccess)
	}

	// Full Access lands while the turn is paused.
	cfg.Values["web_search"] = "live"
	if *turnSettings.ExternalWebAccess.Boolean {
		t.Fatal("the active turn observed the new permission")
	}
	nextSettings := webSearchSettingsFromConfig(cfg, webSearchModeFromConfig(cfg))
	if nextSettings.ExternalWebAccess == nil || nextSettings.ExternalWebAccess.Boolean == nil || !*nextSettings.ExternalWebAccess.Boolean {
		t.Fatalf("next-turn external_web_access = %#v", nextSettings.ExternalWebAccess)
	}
	if nextSettings.ExternalWebAccess == turnSettings.ExternalWebAccess {
		t.Fatal("turn snapshots share the external_web_access pointer")
	}

	// Disabling search mid-turn only removes the next turn's tool.
	cfg.Values["web_search"] = string(codexapi.WebSearchModeDisabled)
	if *turnSettings.ExternalWebAccess.Boolean {
		t.Fatal("the active turn lost its permission snapshot")
	}
	if got := webSearchModeFromConfig(cfg); got != codexapi.WebSearchModeDisabled {
		t.Fatalf("next-turn mode = %q", got)
	}
}
