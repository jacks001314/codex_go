package tui

import "strconv"

// AgentsDebugConfig carries the effective `[agents]` values Rust prints at the
// end of `/debug-config` (debug_config.rs::render_agents_config_lines).
// Nil pointers mean the user never set the key, which Rust renders as <unset>.
type AgentsDebugConfig struct {
	Enabled                        *bool
	MaxConcurrentThreadsPerSession *int
	MaxDepth                       *int
	DefaultSubagentModel           string
	DefaultSubagentReasoningEffort string
	InterruptMessage               *bool
}

// AgentsDebugConfigFromValues reads the merged `agents` table.
func AgentsDebugConfigFromValues(values map[string]any) AgentsDebugConfig {
	var out AgentsDebugConfig
	raw, ok := values["agents"].(map[string]any)
	if !ok {
		return out
	}
	if value, ok := raw["enabled"].(bool); ok {
		out.Enabled = &value
	}
	if value, ok := debugConfigInt(raw["max_concurrent_threads_per_session"]); ok {
		out.MaxConcurrentThreadsPerSession = &value
	} else if value, ok := debugConfigInt(raw["max_threads"]); ok {
		out.MaxConcurrentThreadsPerSession = &value
	}
	if value, ok := debugConfigInt(raw["max_depth"]); ok {
		out.MaxDepth = &value
	}
	if value, ok := raw["default_subagent_model"].(string); ok {
		out.DefaultSubagentModel = value
	}
	if value, ok := raw["default_subagent_reasoning_effort"].(string); ok {
		out.DefaultSubagentReasoningEffort = value
	}
	if value, ok := raw["interrupt_message"].(bool); ok {
		out.InterruptMessage = &value
	}
	return out
}

// RenderAgentsConfigLines mirrors Rust's render_agents_config_lines: a blank
// row, the [agents] header and six bullet lines with Rust's defaults.
func RenderAgentsConfigLines(cfg AgentsDebugConfig) []string {
	optional := func(value string) string {
		if value == "" {
			return "<unset>"
		}
		return value
	}
	boolValue := func(value *bool, fallback bool) string {
		if value == nil {
			return strconv.FormatBool(fallback)
		}
		return strconv.FormatBool(*value)
	}
	maxThreads := "<unset>"
	if cfg.MaxConcurrentThreadsPerSession != nil {
		maxThreads = strconv.Itoa(*cfg.MaxConcurrentThreadsPerSession)
	}
	maxDepth := "1"
	if cfg.MaxDepth != nil {
		maxDepth = strconv.Itoa(*cfg.MaxDepth)
	}
	return []string{
		"",
		"[agents]:",
		"  - enabled = " + boolValue(cfg.Enabled, true),
		"  - max_concurrent_threads_per_session = " + maxThreads,
		"  - max_depth = " + maxDepth + " (V1 only; ignored by V2)",
		"  - default_subagent_model = " + optional(cfg.DefaultSubagentModel),
		"  - default_subagent_reasoning_effort = " + optional(cfg.DefaultSubagentReasoningEffort),
		"  - interrupt_message = " + boolValue(cfg.InterruptMessage, true),
	}
}

func debugConfigInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}
