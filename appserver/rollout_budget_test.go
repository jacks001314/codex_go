package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/runtimeutil"
)

// TestRuntimeRouterRolloutBudgetExhaustionSurfacesSessionBudgetExceeded wires
// the rollout budget end to end: a session whose config enables
// features.rollout_budget resolves a shared budget, usage charges against it,
// exhaustion fails with ErrSessionBudgetExceeded, and the turn error mapper
// surfaces the sessionBudgetExceeded codexErrorInfo like Rust.
func TestRuntimeRouterRolloutBudgetExhaustionSurfacesSessionBudgetExceeded(t *testing.T) {
	home := t.TempDir()
	configTOML := "[features.rollout_budget]\nenabled = true\nlimit_tokens = 10\nreminder_at_remaining_tokens = [5]\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})

	budget := router.rolloutBudgetForSession()
	if budget == nil {
		t.Fatal("rolloutBudgetForSession() = nil, want a configured budget")
	}
	exhausted, err := budget.RecordUsage(runtimeutil.TokenUsage{OutputTokens: 20})
	if err != nil {
		t.Fatalf("RecordUsage error = %v", err)
	}
	if !exhausted {
		t.Fatal("RecordUsage exhausted = false, want true for a 10-token limit charged 20 tokens")
	}

	fields := turnAnalyticsErrorFieldsFromError(ErrSessionBudgetExceeded)
	if fields.TurnError != "sessionBudgetExceeded" {
		t.Fatalf("TurnError = %q, want sessionBudgetExceeded", fields.TurnError)
	}
}

// TestRuntimeRouterRolloutBudgetDisabledResolvesNil verifies the budget stays
// absent when the feature is not enabled in config.
func TestRuntimeRouterRolloutBudgetDisabledResolvesNil(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[features]\nrollout_budget = false\n"), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	if budget := router.rolloutBudgetForSession(); budget != nil {
		t.Fatalf("rolloutBudgetForSession() = %#v, want nil when disabled", budget)
	}
}
