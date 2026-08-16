package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/runtimeutil"
	"codex_go/turn"
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

func expectReminder(t *testing.T, followUp turn.SamplingFollowUp, outputTokens int64, wantRemaining string) {
	t.Helper()
	items := followUp(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{OutputTokens: outputTokens}})
	if len(items) != 1 {
		t.Fatalf("follow-up items = %d, want 1 reminder", len(items))
	}
	encoded, err := json.Marshal(items[0])
	if err != nil {
		t.Fatalf("marshal reminder item: %v", err)
	}
	var item struct {
		Role    string `json:"role"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(encoded, &item); err != nil {
		t.Fatalf("unmarshal reminder item: %v", err)
	}
	text := item.Role + " " + item.Content[0].Text
	if !strings.Contains(text, "<rollout_budget>") ||
		!strings.Contains(text, "You have "+wantRemaining+" weighted tokens left in the shared session token budget.") ||
		!strings.HasPrefix(item.Role, "developer") {
		t.Fatalf("reminder item = %s, want Rust-shaped rollout budget developer message", text)
	}
}

// TestRuntimeRouterRolloutBudgetReminderInjectsDeveloperMessage wires the
// reminder path: after charging usage that crosses a remaining-tokens
// threshold, the sampling follow-up injects the Rust-shaped <rollout_budget>
// developer message exactly once.
func TestRuntimeRouterRolloutBudgetReminderInjectsDeveloperMessage(t *testing.T) {
	home := t.TempDir()
	configTOML := "[features.rollout_budget]\nenabled = true\nlimit_tokens = 100\nreminder_at_remaining_tokens = [75, 50]\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	if router.rolloutBudgetForSession() == nil {
		t.Fatal("rolloutBudgetForSession() = nil")
	}

	followUp := router.rolloutBudgetFollowUp("thread-reminder")
	// The reminder fires on the first charge and at each threshold crossing,
	// exactly once per delivered index (mirrors Rust pending_reminder):
	// 80 remaining (initial), then 75 (crosses the 75 threshold), then 35.
	expectReminder(t, followUp, 20, "80")
	expectReminder(t, followUp, 5, "75")
	if items := followUp(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{OutputTokens: 20}}); len(items) != 0 {
		t.Fatalf("reminder fired without a new threshold crossing: %#v", items)
	}
	expectReminder(t, followUp, 20, "35")
	// The same delivered index is never repeated.
	if items := followUp(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{OutputTokens: 0}}); len(items) != 0 {
		t.Fatalf("duplicate reminder delivered: %#v", items)
	}
	// Exhaustion is recorded on the router and surfaces at turn completion.
	followUp(&turn.SamplingFollowUpContext{Usage: model.AgentUsage{OutputTokens: 60}})
	if !router.rolloutBudgetExhausted.Load() {
		t.Fatal("rolloutBudgetExhausted = false after charging past the limit")
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
