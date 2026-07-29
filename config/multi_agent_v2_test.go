package config

import (
	"strings"
	"testing"
	"time"
)

func TestMultiAgentV2ConfigDefaultsAndOverrides(t *testing.T) {
	defaults, err := (&Config{}).MultiAgentV2Config(0)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.MaxConcurrentThreadsPerSession != 4 || defaults.ToolNamespace != "collaboration" ||
		defaults.MinWaitTimeout != 10*time.Second || defaults.DefaultWaitTimeout != 30*time.Second || defaults.MaxWaitTimeout != time.Hour ||
		!defaults.HideSpawnAgentMetadata || !defaults.ExposeSpawnAgentModelOverrides || !defaults.WaitAgentEnabled || !defaults.NonCodeModeOnly {
		t.Fatalf("defaults = %#v", defaults)
	}
	cfg := &Config{Values: map[string]any{"features": map[string]any{"multi_agent_v2": map[string]any{
		"max_concurrent_threads_per_session": int64(5), "min_wait_timeout_ms": int64(0), "max_wait_timeout_ms": int64(100),
		"default_wait_timeout_ms": int64(50), "tool_namespace": "agents", "hide_spawn_agent_metadata": false,
		"expose_spawn_agent_model_overrides": false, "wait_agent_enabled": false, "non_code_mode_only": false,
		"subagent_developer_instructions": "  child only  ",
	}}}}
	got, err := cfg.MultiAgentV2Config(3)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxConcurrentThreadsPerSession != 5 || got.MinWaitTimeout != 0 || got.DefaultWaitTimeout != 50*time.Millisecond || got.MaxWaitTimeout != 100*time.Millisecond ||
		got.ToolNamespace != "agents" || got.HideSpawnAgentMetadata || got.ExposeSpawnAgentModelOverrides || got.WaitAgentEnabled || got.NonCodeModeOnly || got.SubagentDeveloperInstructions == nil || *got.SubagentDeveloperInstructions != "child only" {
		t.Fatalf("overrides = %#v", got)
	}
	empty, err := (&Config{Values: map[string]any{"features": map[string]any{"multi_agent_v2": map[string]any{"subagent_developer_instructions": "  "}}}}).MultiAgentV2Config(0)
	if err != nil || empty.SubagentDeveloperInstructions == nil || *empty.SubagentDeveloperInstructions != "" {
		t.Fatalf("empty override = %#v, %v", empty, err)
	}
}

func TestMultiAgentV2ConfigAddsRootToLegacyAgentLimit(t *testing.T) {
	for _, key := range []string{"max_concurrent_threads_per_session", "max_threads"} {
		cfg := &Config{Values: map[string]any{"agents": map[string]any{key: int64(4)}}}
		agents, err := cfg.AgentsConfig(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		got, err := cfg.MultiAgentV2Config(agents.MaxConcurrentThreadsPerSession)
		if err != nil {
			t.Fatal(err)
		}
		if got.MaxConcurrentThreadsPerSession != 5 {
			t.Fatalf("%s V2 max concurrency = %d, want 5", key, got.MaxConcurrentThreadsPerSession)
		}
	}
}

func TestMultiAgentV2ConfigRejectsInvalidBoundsAndNamespace(t *testing.T) {
	for _, raw := range []map[string]any{
		{"min_wait_timeout_ms": int64(100), "max_wait_timeout_ms": int64(50)},
		{"default_wait_timeout_ms": int64(3_600_001)},
		{"tool_namespace": "functions"},
		{"tool_namespace": "bad namespace"},
	} {
		cfg := &Config{Values: map[string]any{"features": map[string]any{"multi_agent_v2": raw}}}
		if _, err := cfg.MultiAgentV2Config(4); err == nil || !strings.Contains(err.Error(), "features.multi_agent_v2") {
			t.Fatalf("raw=%#v error=%v", raw, err)
		}
	}
}
