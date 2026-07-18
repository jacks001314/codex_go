package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentsConfigParsesRustShapeAndCanonicalLimitWins(t *testing.T) {
	cfg := &Config{Values: map[string]any{"agents": map[string]any{
		"enabled": true, "max_threads": int64(3), "max_concurrent_threads_per_session": int64(9),
		"max_depth": int64(2), "default_subagent_model": " gpt-worker ",
		"default_subagent_reasoning_effort": " high ", "job_max_runtime_seconds": int64(120),
		"interrupt_message": false,
		"researcher":        map[string]any{"description": " Research code. ", "nickname_candidates": []any{"Scout", "Atlas"}},
	}}}
	got, err := cfg.AgentsConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled == nil || !*got.Enabled || got.MaxConcurrentThreadsPerSession != 9 || got.MaxDepth == nil || *got.MaxDepth != 2 || got.DefaultSubagentModel != "gpt-worker" || got.DefaultSubagentReasoningEffort != "high" || got.JobMaxRuntimeSeconds == nil || *got.JobMaxRuntimeSeconds != 120 || got.InterruptMessage == nil || *got.InterruptMessage {
		t.Fatalf("AgentsConfig() = %+v", got)
	}
	role := got.Roles["researcher"]
	if role.Description != "Research code." || strings.Join(role.NicknameCandidates, ",") != "Scout,Atlas" {
		t.Fatalf("role = %+v", role)
	}
}

func TestAgentsConfigLoadsRelativeRoleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "roles", "reviewer.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "description = \"Review changes.\"\nnickname_candidates = [\"Sage\"]\nmodel = \"gpt-review\"\nmodel_reasoning_effort = \"high\"\ndeveloper_instructions = \"Review carefully.\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Values: map[string]any{"agents": map[string]any{"reviewer": map[string]any{"config_file": filepath.Join("roles", "reviewer.toml")}}}}
	got, err := cfg.AgentsConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	role := got.Roles["reviewer"]
	if role.ConfigFile != path || role.Description != "Review changes." || role.Settings["model"] != "gpt-review" || role.Settings["developer_instructions"] != "Review carefully." {
		t.Fatalf("role = %+v", role)
	}
}

func TestAgentsConfigRejectsInvalidRoles(t *testing.T) {
	for _, tc := range []struct {
		name string
		role map[string]any
		want string
	}{
		{"missing description", map[string]any{}, "must define a description"},
		{"blank description", map[string]any{"description": "  "}, "cannot be blank"},
		{"empty nicknames", map[string]any{"description": "x", "nickname_candidates": []any{}}, "at least one"},
		{"duplicate nicknames", map[string]any{"description": "x", "nickname_candidates": []any{"A", "A"}}, "duplicates"},
		{"invalid nickname", map[string]any{"description": "x", "nickname_candidates": []any{"A!"}}, "ASCII"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Values: map[string]any{"agents": map[string]any{"worker": tc.role}}}
			_, err := cfg.AgentsConfig(t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestAgentsConfigRejectsMissingOrDirectoryRoleFile(t *testing.T) {
	for _, path := range []string{"missing.toml", "."} {
		cfg := &Config{Values: map[string]any{"agents": map[string]any{"worker": map[string]any{"description": "x", "config_file": path}}}}
		if _, err := cfg.AgentsConfig(t.TempDir()); err == nil {
			t.Fatalf("config_file %q unexpectedly accepted", path)
		}
	}
}

func TestAgentsConfigRejectsInvalidPositiveLimits(t *testing.T) {
	for _, key := range []string{"max_concurrent_threads_per_session", "max_threads", "job_max_runtime_seconds"} {
		t.Run(key, func(t *testing.T) {
			cfg := &Config{Values: map[string]any{"agents": map[string]any{key: int64(0)}}}
			_, err := cfg.AgentsConfig(t.TempDir())
			if err == nil || !strings.Contains(err.Error(), "must be at least 1") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
