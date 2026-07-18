package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codex_go/internal/agent"

	"github.com/pelletier/go-toml/v2"
)

const DefaultAgentMaxConcurrentThreadsPerSession = 6

type AgentsConfig struct {
	Enabled                        *bool
	MaxConcurrentThreadsPerSession int
	MaxDepth                       *int
	DefaultSubagentModel           string
	DefaultSubagentReasoningEffort string
	JobMaxRuntimeSeconds           *int
	InterruptMessage               *bool
	Roles                          map[string]agent.RoleConfig
}

func (c *Config) AgentsConfig(configBaseDir string) (*AgentsConfig, error) {
	out := &AgentsConfig{MaxConcurrentThreadsPerSession: DefaultAgentMaxConcurrentThreadsPerSession, Roles: map[string]agent.RoleConfig{}}
	if c == nil || c.Values == nil {
		return out, nil
	}
	raw, ok := c.Values["agents"].(map[string]any)
	if !ok {
		return out, nil
	}
	if enabled, ok := raw["enabled"].(bool); ok {
		out.Enabled = &enabled
	}
	if rawValue, exists := raw["max_concurrent_threads_per_session"]; exists {
		value, ok := configPositiveInt(rawValue)
		if !ok {
			return nil, fmt.Errorf("agents.max_concurrent_threads_per_session must be at least 1")
		}
		out.MaxConcurrentThreadsPerSession = value
	} else if rawValue, exists := raw["max_threads"]; exists {
		value, ok := configPositiveInt(rawValue)
		if !ok {
			return nil, fmt.Errorf("agents.max_concurrent_threads_per_session must be at least 1")
		}
		out.MaxConcurrentThreadsPerSession = value
	}

	if value, ok := configInt(raw["max_depth"]); ok {
		out.MaxDepth = &value
	}
	if value, ok := raw["default_subagent_model"].(string); ok {
		out.DefaultSubagentModel = strings.TrimSpace(value)
	}
	if value, ok := raw["default_subagent_reasoning_effort"].(string); ok {
		out.DefaultSubagentReasoningEffort = strings.TrimSpace(value)
	}
	if rawValue, exists := raw["job_max_runtime_seconds"]; exists {
		value, ok := configPositiveInt(rawValue)
		if !ok {
			return nil, fmt.Errorf("agents.job_max_runtime_seconds must be at least 1")
		}
		out.JobMaxRuntimeSeconds = &value
	}
	if value, ok := raw["interrupt_message"].(bool); ok {
		out.InterruptMessage = &value
	}
	reserved := map[string]bool{
		"enabled": true, "max_concurrent_threads_per_session": true, "max_threads": true,
		"max_depth": true, "default_subagent_model": true,
		"default_subagent_reasoning_effort": true, "job_max_runtime_seconds": true,
		"interrupt_message": true,
	}
	for name, value := range raw {
		if reserved[name] {
			continue
		}
		role, err := parseAgentRoleConfig(name, value, configBaseDir)
		if err != nil {
			return nil, err
		}
		out.Roles[name] = role
	}
	return out, nil
}

func parseAgentRoleConfig(name string, value any, configBaseDir string) (agent.RoleConfig, error) {
	table, ok := value.(map[string]any)
	if !ok {
		return agent.RoleConfig{}, fmt.Errorf("agents.%s must be a table", name)
	}
	role := agent.RoleConfig{}
	if description, ok := table["description"].(string); ok {
		role.Description = strings.TrimSpace(description)
		if role.Description == "" {
			return agent.RoleConfig{}, fmt.Errorf("agents.%s.description cannot be blank", name)
		}
	}
	if nicknames, exists := table["nickname_candidates"]; exists {
		normalized, err := normalizeAgentNicknames("agents."+name+".nickname_candidates", nicknames)
		if err != nil {
			return agent.RoleConfig{}, err
		}
		role.NicknameCandidates = normalized
	}
	if path, ok := table["config_file"].(string); ok && strings.TrimSpace(path) != "" {
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) {
			path = filepath.Join(configBaseDir, path)
		}
		path = filepath.Clean(path)
		info, err := os.Stat(path)
		if err != nil {
			return agent.RoleConfig{}, fmt.Errorf("agents.%s.config_file must point to an existing file at %s: %w", name, path, err)
		}
		if !info.Mode().IsRegular() {
			return agent.RoleConfig{}, fmt.Errorf("agents.%s.config_file must point to a file: %s", name, path)
		}
		fileRole, err := parseAgentRoleFile(path, name)
		if err != nil {
			return agent.RoleConfig{}, err
		}
		role.ConfigFile = path
		if fileRole.Description != "" {
			role.Description = fileRole.Description
		}
		if fileRole.NicknameCandidates != nil {
			role.NicknameCandidates = fileRole.NicknameCandidates
		}
		role.Settings = fileRole.Settings
	}
	if role.Description == "" {
		return agent.RoleConfig{}, fmt.Errorf("agent role `%s` must define a description", name)
	}
	return role, nil
}

func parseAgentRoleFile(path string, roleName string) (agent.RoleConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return agent.RoleConfig{}, err
	}
	var values map[string]any
	if err := toml.Unmarshal(data, &values); err != nil {
		return agent.RoleConfig{}, fmt.Errorf("failed to parse agent role file at %s: %w", path, err)
	}
	role := agent.RoleConfig{Settings: map[string]string{}}
	if description, ok := values["description"].(string); ok {
		role.Description = strings.TrimSpace(description)
		if role.Description == "" {
			return agent.RoleConfig{}, fmt.Errorf("agent role file %s.description cannot be blank", path)
		}
	}
	if nicknames, exists := values["nickname_candidates"]; exists {
		role.NicknameCandidates, err = normalizeAgentNicknames("agent role file "+path+".nickname_candidates", nicknames)
		if err != nil {
			return agent.RoleConfig{}, err
		}
	}
	for _, key := range []string{"model", "model_provider", "model_reasoning_effort", "service_tier", "developer_instructions"} {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			role.Settings[key] = strings.TrimSpace(value)
		}
	}
	if value, ok := values["developer_instructions"].(string); ok && strings.TrimSpace(value) == "" {
		return agent.RoleConfig{}, fmt.Errorf("agent role file at %s.developer_instructions cannot be blank", path)
	}
	if len(role.Settings) == 0 {
		role.Settings = nil
	}
	_ = roleName
	return role, nil
}

func normalizeAgentNicknames(label string, value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		if stringsValue, stringsOK := value.([]string); stringsOK {
			items = make([]any, len(stringsValue))
			for i := range stringsValue {
				items[i] = stringsValue[i]
			}
		} else {
			return nil, fmt.Errorf("%s must contain at least one name", label)
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%s must contain at least one name", label)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("%s cannot contain blank names", label)
		}
		name = strings.TrimSpace(name)
		if seen[name] {
			return nil, fmt.Errorf("%s cannot contain duplicates", label)
		}
		for _, char := range name {
			if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == ' ' || char == '-' || char == '_') {
				return nil, fmt.Errorf("%s may only contain ASCII letters, digits, spaces, hyphens, and underscores", label)
			}
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

func configPositiveInt(value any) (int, bool) {
	parsed, ok := configInt(value)
	return parsed, ok && parsed > 0
}

func configInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int64:
		return int(typed), true
	case int:
		return typed, true
	case float64:
		return int(typed), typed == float64(int(typed))
	default:
		return 0, false
	}
}
