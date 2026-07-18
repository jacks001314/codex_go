package config

import (
	"fmt"
	"strings"
)

const AutoCompactFallbackPromptMaxBytes = 2000

type TokenBudgetConfig struct {
	Enabled                         bool
	ReminderThresholdTokens         *int
	ReminderMessageTemplate         string
	GuidanceMessage                 string
	AutoCompactFallbackPrompt       string
	AutoCompactFallbackBufferTokens *int
}

func (c *Config) TokenBudgetConfig() (*TokenBudgetConfig, error) {
	out := &TokenBudgetConfig{}
	if c == nil || c.Values == nil {
		return out, nil
	}
	features, _ := c.Values["features"].(map[string]any)
	raw := features["token_budget"]
	if enabled, ok := raw.(bool); ok {
		out.Enabled = enabled
		return out, nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return out, nil
	}
	out.Enabled, _ = table["enabled"].(bool)
	if value, ok := configPositiveInt(table["reminder_threshold_tokens"]); ok {
		out.ReminderThresholdTokens = &value
	}
	if value, ok := table["reminder_message_template"].(string); ok {
		out.ReminderMessageTemplate = strings.TrimSpace(value)
	}
	if value, ok := table["guidance_message"].(string); ok {
		out.GuidanceMessage = strings.TrimSpace(value)
	}
	if value, ok := table["auto_compact_fallback_prompt"].(string); ok {
		out.AutoCompactFallbackPrompt = strings.TrimSpace(value)
	}
	if len(out.AutoCompactFallbackPrompt) > AutoCompactFallbackPromptMaxBytes {
		return nil, fmt.Errorf("features.token_budget.auto_compact_fallback_prompt must not exceed %d bytes", AutoCompactFallbackPromptMaxBytes)
	}
	if rawBuffer, exists := table["auto_compact_fallback_buffer_tokens"]; exists {
		value, ok := configPositiveInt(rawBuffer)
		if !ok {
			return nil, fmt.Errorf("features.token_budget.auto_compact_fallback_buffer_tokens must be positive")
		}
		out.AutoCompactFallbackBufferTokens = &value
	}
	if out.AutoCompactFallbackPrompt != "" && out.AutoCompactFallbackBufferTokens == nil {
		return nil, fmt.Errorf("features.token_budget.auto_compact_fallback_buffer_tokens is required when auto_compact_fallback_prompt is set")
	}
	return out, nil
}
