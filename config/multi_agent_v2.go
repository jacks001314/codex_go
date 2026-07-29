package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultMultiAgentV2MaxConcurrentThreads = 4
	DefaultMultiAgentV2MinWait              = 10 * time.Second
	DefaultMultiAgentV2MaxWait              = time.Hour
	DefaultMultiAgentV2Wait                 = 30 * time.Second
)

type MultiAgentV2Config struct {
	MaxConcurrentThreadsPerSession int
	MinWaitTimeout                 time.Duration
	MaxWaitTimeout                 time.Duration
	DefaultWaitTimeout             time.Duration
	ToolNamespace                  string
	HideSpawnAgentMetadata         bool
	ExposeSpawnAgentModelOverrides bool
	WaitAgentEnabled               bool
	NonCodeModeOnly                bool
	SubagentDeveloperInstructions  *string
}

func (c *Config) MultiAgentV2Config(agentsMax int) (*MultiAgentV2Config, error) {
	maxConcurrency := DefaultMultiAgentV2MaxConcurrentThreads
	if agentsMax > 0 && hasLegacyAgentMaxThreads(c) {
		// The legacy agents limit counts children; V2 counts the root as a slot.
		maxConcurrency = agentsMax + 1
	}
	out := &MultiAgentV2Config{
		MaxConcurrentThreadsPerSession: maxConcurrency,
		MinWaitTimeout:                 DefaultMultiAgentV2MinWait,
		MaxWaitTimeout:                 DefaultMultiAgentV2MaxWait,
		DefaultWaitTimeout:             DefaultMultiAgentV2Wait,
		ToolNamespace:                  "collaboration",
		HideSpawnAgentMetadata:         true,
		ExposeSpawnAgentModelOverrides: true,
		WaitAgentEnabled:               true,
		NonCodeModeOnly:                true,
	}
	if c == nil || c.Values == nil {
		return out, nil
	}
	features, _ := c.Values["features"].(map[string]any)
	raw, _ := features["multi_agent_v2"].(map[string]any)
	if raw == nil {
		return out, nil
	}
	if value, ok := configInt(raw["max_concurrent_threads_per_session"]); ok {
		if value < 1 {
			return nil, fmt.Errorf("features.multi_agent_v2.max_concurrent_threads_per_session must be at least 1")
		}
		out.MaxConcurrentThreadsPerSession = value
	}
	for key, target := range map[string]*time.Duration{
		"min_wait_timeout_ms": &out.MinWaitTimeout, "max_wait_timeout_ms": &out.MaxWaitTimeout, "default_wait_timeout_ms": &out.DefaultWaitTimeout,
	} {
		if value, exists := raw[key]; exists {
			parsed, ok := configInt(value)
			if !ok || parsed < 0 {
				return nil, fmt.Errorf("features.multi_agent_v2.%s must be at least 0", key)
			}
			if parsed > int(time.Hour/time.Millisecond) {
				return nil, fmt.Errorf("features.multi_agent_v2.%s must be at most 3600000", key)
			}
			*target = time.Duration(parsed) * time.Millisecond
		}
	}
	if out.MinWaitTimeout > out.MaxWaitTimeout {
		return nil, fmt.Errorf("features.multi_agent_v2.min_wait_timeout_ms must be at most features.multi_agent_v2.max_wait_timeout_ms")
	}
	if out.DefaultWaitTimeout < out.MinWaitTimeout {
		return nil, fmt.Errorf("features.multi_agent_v2.default_wait_timeout_ms must be at least features.multi_agent_v2.min_wait_timeout_ms")
	}
	if out.DefaultWaitTimeout > out.MaxWaitTimeout {
		return nil, fmt.Errorf("features.multi_agent_v2.default_wait_timeout_ms must be at most features.multi_agent_v2.max_wait_timeout_ms")
	}
	if value, ok := raw["tool_namespace"].(string); ok {
		if err := validateMultiAgentV2Namespace(value); err != nil {
			return nil, err
		}
		out.ToolNamespace = value
	}
	if value, ok := raw["subagent_developer_instructions"].(string); ok {
		trimmed := strings.TrimSpace(value)
		out.SubagentDeveloperInstructions = &trimmed
	}
	for key, target := range map[string]*bool{
		"hide_spawn_agent_metadata":          &out.HideSpawnAgentMetadata,
		"expose_spawn_agent_model_overrides": &out.ExposeSpawnAgentModelOverrides,
		"wait_agent_enabled":                 &out.WaitAgentEnabled,
		"non_code_mode_only":                 &out.NonCodeModeOnly,
	} {
		if value, ok := raw[key].(bool); ok {
			*target = value
		}
	}
	return out, nil
}

func hasLegacyAgentMaxThreads(c *Config) bool {
	if c == nil || c.Values == nil {
		return false
	}
	agents, _ := c.Values["agents"].(map[string]any)
	if agents == nil {
		return false
	}
	_, hasCurrent := agents["max_concurrent_threads_per_session"]
	_, hasLegacy := agents["max_threads"]
	return hasCurrent || hasLegacy
}

var multiAgentV2NamespacePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func validateMultiAgentV2Namespace(value string) error {
	const label = "features.multi_agent_v2.tool_namespace"
	if value == "" || strings.TrimSpace(value) != value || len(value) > 64 || !multiAgentV2NamespacePattern.MatchString(value) {
		return fmt.Errorf("%s must match ^[a-zA-Z0-9_-]+$", label)
	}
	reserved := map[string]bool{
		"api_tool": true, "browser": true, "computer": true, "container": true, "file_search": true,
		"functions": true, "image_gen": true, "multi_tool_use": true, "python": true, "python_user_visible": true,
		"submodel_delegator": true, "terminal": true, "tool_search": true, "web": true,
	}
	if reserved[value] {
		return fmt.Errorf("%s uses a reserved namespace: %s", label, value)
	}
	return nil
}
