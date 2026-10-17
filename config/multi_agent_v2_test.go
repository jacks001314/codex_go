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

// Rust parity: features.multi_agent_v2.message_board_in_memory defaults to
// false and keeps the board in memory (including ephemeral sessions) when set.
func TestMultiAgentV2ConfigMessageBoardInMemoryLikeRust(t *testing.T) {
	defaults, err := (&Config{}).MultiAgentV2Config(0)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.MessageBoardInMemory {
		t.Fatalf("default message_board_in_memory = true, want false")
	}
	cfg := &Config{Values: map[string]any{"features": map[string]any{"multi_agent_v2": map[string]any{
		"message_board_in_memory": true,
	}}}}
	got, err := cfg.MultiAgentV2Config(0)
	if err != nil {
		t.Fatal(err)
	}
	if !got.MessageBoardInMemory {
		t.Fatalf("message_board_in_memory = false, want true")
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

// Rust parity (#53617): features.multi_agent_v2.disable_native_tools defaults
// to false and keeps the V2 runtime enabled while hiding native tools.
func TestMultiAgentV2ConfigDisableNativeToolsLikeRust(t *testing.T) {
	defaults, err := (&Config{}).MultiAgentV2Config(0)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.DisableNativeTools {
		t.Fatalf("default disable_native_tools = true, want false")
	}
	cfg := &Config{Values: map[string]any{"features": map[string]any{"multi_agent_v2": map[string]any{
		"disable_native_tools": true,
	}}}}
	got, err := cfg.MultiAgentV2Config(0)
	if err != nil {
		t.Fatal(err)
	}
	if !got.DisableNativeTools {
		t.Fatalf("disable_native_tools = false, want true")
	}
	// Hiding native tools must not disturb the rest of the V2 runtime surface.
	if !got.WaitAgentEnabled || got.ToolNamespace != "collaboration" {
		t.Fatalf("disable_native_tools changed unrelated v2 settings: %#v", got)
	}
	errCfg := &Config{Values: map[string]any{"features": map[string]any{"multi_agent_v2": map[string]any{
		"disable_native_tools": true,
		"min_wait_timeout_ms":  int64(2000),
		"max_wait_timeout_ms":  int64(1000),
	}}}}
	if _, err := errCfg.MultiAgentV2Config(0); err == nil {
		t.Fatalf("MultiAgentV2Config() accepted an inverted wait window")
	}
}

// Rust parity (#47540): features.multi_agent_v2.disable_direct_message
// defaults to false and hides send_message/followup_task when enabled.
func TestMultiAgentV2ConfigDisableDirectMessageLikeRust(t *testing.T) {
	defaults, err := (&Config{}).MultiAgentV2Config(0)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.DisableDirectMessage {
		t.Fatalf("default disable_direct_message = true, want false")
	}
	cfg := &Config{Values: map[string]any{"features": map[string]any{"multi_agent_v2": map[string]any{
		"disable_direct_message": true,
	}}}}
	got, err := cfg.MultiAgentV2Config(0)
	if err != nil {
		t.Fatal(err)
	}
	if !got.DisableDirectMessage {
		t.Fatalf("disable_direct_message = false, want true")
	}
	if got.DisableNativeTools || !got.WaitAgentEnabled {
		t.Fatalf("disable_direct_message changed unrelated settings: %#v", got)
	}
}
