package exec

import (
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
)

// TestAgentForRunAppliesManagedResidencyLikeRust mirrors Rust's exec startup,
// which installs the managed `enforce_residency` requirement so every model
// request carries the internal residency header.
func TestAgentForRunAppliesManagedResidencyLikeRust(t *testing.T) {
	residency := config.ResidencyUS
	cfg := &config.Config{
		Values:       map[string]any{},
		Requirements: &config.ConfigRequirements{EnforceResidency: &residency},
	}
	resolved := &auth.ResolvedAuth{Auth: auth.FromAPIKey("sk-test")}
	runner := &Runner{UseResponsesAPI: true}
	agent, err := runner.agentForRun(cfg, resolved, "openai", nil)
	if err != nil {
		t.Fatalf("agentForRun() error = %v", err)
	}
	responsesAgent, ok := agent.(*model.ResponsesAgentRunner)
	if !ok {
		t.Fatalf("agent = %T, want *model.ResponsesAgentRunner", agent)
	}
	if responsesAgent.Residency != "us" {
		t.Fatalf("agent residency = %q, want us", responsesAgent.Residency)
	}

	// Without a requirement the runner keeps no residency.
	plain, err := runner.agentForRun(&config.Config{Values: map[string]any{}}, resolved, "openai", nil)
	if err != nil {
		t.Fatalf("agentForRun() error = %v", err)
	}
	if got := plain.(*model.ResponsesAgentRunner).Residency; got != "" {
		t.Fatalf("agent residency = %q, want empty", got)
	}
}

// TestManagedResidencyForConfigLikeRust covers the requirement lookup.
func TestManagedResidencyForConfigLikeRust(t *testing.T) {
	if got := managedResidencyForConfig(nil); got != "" {
		t.Fatalf("nil config residency = %q, want empty", got)
	}
	if got := managedResidencyForConfig(&config.Config{Values: map[string]any{}}); got != "" {
		t.Fatalf("config without requirements residency = %q, want empty", got)
	}
	residency := config.ResidencyUS
	cfg := &config.Config{Values: map[string]any{}, Requirements: &config.ConfigRequirements{EnforceResidency: &residency}}
	if got := managedResidencyForConfig(cfg); got != "us" {
		t.Fatalf("config residency = %q, want us", got)
	}
}
