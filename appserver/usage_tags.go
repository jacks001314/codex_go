package appserver

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
)

// usageTagFeatureExclusions are the features whose tags Rust omits from the
// usage-tag document (Feature::Collab and Feature::SpawnCsv).
var usageTagFeatureExclusions = map[string]bool{
	"multi_agent":   true,
	"enable_fanout": true,
}

// UsageTagsForTurn mirrors Rust core::feedback_config::usage_tags (#46501): a
// searchable, scalar-only diagnostics document built from an explicit
// configuration allowlist plus one tag per reported feature. It never includes
// prompts, paths, credentials, provider endpoints or the raw configuration.
//
// Configured overrides stay separate from the step's resolved model limits, and
// an explicit unset overwrites a prior turn's override in the collector.
// featureSettings is the turn's resolved feature enablement (Rust's `Features`).
func UsageTagsForTurn(cfg *config.Config, featureSettings map[string]bool, modelInfo *model.ModelInfo, serviceTier string) map[string]string {
	values := map[string]any(nil)
	if cfg != nil {
		values = cfg.Values
	}
	contextWindow, hasContextWindow := usageTagPath(values, "model_context_window")
	contextWindowEffective := any(nil)
	if modelInfo != nil {
		if window, ok := modelInfo.UsableContextWindow(); ok {
			contextWindowEffective = window
		}
	}
	tokenBudget := usageTagMap(values, "token_budget")
	rolloutBudget := usageTagMap(values, "rollout_budget")
	memories := usageTagMap(values, "memories")
	multiAgentV2 := usageTagMap(values, "multi_agent_v2")
	tags := map[string]string{
		"model_context_window":                 usageTagText(contextWindow),
		"model_context_window_override":        usageTagBool(hasContextWindow),
		"model_context_window_effective":       usageTagText(contextWindowEffective),
		"model_auto_compact_token_limit":       usageTagText(usageTagPathValue(values, "model_auto_compact_token_limit")),
		"model_auto_compact_token_limit_scope": usageTagText(usageTagPathValue(values, "model_auto_compact_token_limit_scope")),
		"tool_output_token_limit":              usageTagText(toolOutputTokenLimitValue(cfg)),
		"project_doc_max_bytes":                usageTagText(projectDocMaxBytesValue(cfg)),
		"model_verbosity":                      usageTagText(usageTagPathValue(values, "model_verbosity")),
		"model_reasoning_summary":              usageTagText(usageTagPathValue(values, "model_reasoning_summary")),
		// Rust passes Option<&str>: an absent tier is the "unset" marker, and
		// Go represents absent as the empty string.
		"service_tier":                                      usageTagText(usageTagOptionalString(serviceTier)),
		"review_model":                                      usageTagText(usageTagPathValue(values, "review_model")),
		"agent_default_subagent_model":                      usageTagText(usageTagPathValue(values, "agent_default_subagent_model")),
		"agent_default_subagent_reasoning_effort":           usageTagText(usageTagPathValue(values, "agent_default_subagent_reasoning_effort")),
		"multi_agent_v2.max_concurrent_threads_per_session": usageTagText(multiAgentV2["max_concurrent_threads_per_session"]),
		"multi_agent_v2.wait_agent_enabled":                 usageTagText(multiAgentV2["wait_agent_enabled"]),
		"max_goal_token_budget":                             usageTagText(usageTagPathValue(values, "max_goal_token_budget")),
		"memories.generate_memories":                        usageTagText(memories["generate_memories"]),
		"memories.use_memories":                             usageTagText(memories["use_memories"]),
		"memories.max_rollouts_per_startup":                 usageTagText(memories["max_rollouts_per_startup"]),
		"memories.max_raw_memories_for_consolidation":       usageTagText(memories["max_raw_memories_for_consolidation"]),
		"memories.extract_model":                            usageTagText(memories["extract_model"]),
		"memories.consolidation_model":                      usageTagText(memories["consolidation_model"]),
		"token_budget.present":                              usageTagBool(tokenBudget != nil),
		"token_budget.reminder_threshold_tokens":            usageTagText(tokenBudget["reminder_threshold_tokens"]),
		"token_budget.auto_compact_fallback_buffer_tokens":  usageTagText(tokenBudget["auto_compact_fallback_buffer_tokens"]),
		"rollout_budget.limit_tokens":                       usageTagText(rolloutBudget["limit_tokens"]),
		"rollout_budget.sampling_token_weight":              usageTagText(rolloutBudget["sampling_token_weight"]),
		"rollout_budget.prefill_token_weight":               usageTagText(rolloutBudget["prefill_token_weight"]),
	}
	for _, spec := range features.Registry {
		if usageTagFeatureExclusions[spec.Key] {
			continue
		}
		tags["feature."+spec.Key] = usageTagBool(features.Enabled(featureSettings, spec.Key))
	}
	return tags
}

// UsageTagsJSON renders the tag document the sampling span carries as
// `tags_json` (Rust records the serialized map in the span field).
func UsageTagsJSON(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(tags))
	for _, key := range keys {
		ordered[key] = tags[key]
	}
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// usageTagText renders one allowlisted value the way Rust's json! macro plus
// to_string() does: an absent or null value is the explicit "unset" marker,
// strings stay verbatim, and scalars print canonically.
func usageTagText(value any) string {
	switch typed := value.(type) {
	case nil:
		return "unset"
	case string:
		return typed
	case bool:
		return usageTagBool(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	case map[string]any, []any:
		if encoded, err := json.Marshal(typed); err == nil {
			return string(encoded)
		}
	}
	return strings.TrimSpace(usageTagTextScalar(value))
}

func usageTagTextScalar(value any) string {
	if encoded, err := json.Marshal(value); err == nil {
		return string(encoded)
	}
	return ""
}

func usageTagBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// usageTagOptionalString maps Go's empty-string convention to Rust's Option.
func usageTagOptionalString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// usageTagPath resolves a nested configuration value; ok reports whether the key
// is present at all (Rust's Option::is_some for the override marker).
func usageTagPath(values map[string]any, keys ...string) (any, bool) {
	current := any(values)
	for _, key := range keys {
		table, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, present := table[key]
		if !present {
			return nil, false
		}
		current = value
	}
	return current, true
}

func usageTagPathValue(values map[string]any, keys ...string) any {
	value, _ := usageTagPath(values, keys...)
	return value
}

// usageTagMap returns a nested configuration table, or nil when it is absent.
func usageTagMap(values map[string]any, keys ...string) map[string]any {
	value, ok := usageTagPath(values, keys...)
	if !ok {
		return nil
	}
	table, _ := value.(map[string]any)
	return table
}

func toolOutputTokenLimitValue(cfg *config.Config) any {
	if cfg == nil {
		return nil
	}
	if limit := cfg.ToolOutputTokenLimit(); limit != nil {
		return *limit
	}
	return nil
}

func projectDocMaxBytesValue(cfg *config.Config) any {
	if cfg == nil {
		return nil
	}
	if _, present := usageTagPath(cfg.Values, "project_doc_max_bytes"); !present {
		return nil
	}
	return cfg.ProjectDocMaxBytes()
}
