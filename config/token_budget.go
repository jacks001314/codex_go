package config

import (
	"fmt"
	"strings"
)

const (
	TokenBudgetReminderMessageTemplateMaxBytes = 2000
	TokenBudgetGuidanceMessageMaxBytes         = 2000
	AutoCompactFallbackPromptMaxBytes          = 2000
	DefaultTokenBudgetReminderMessageTemplate  = "Your context window is nearly exhausted (only {n_remaining} tokens remaining) and will be automatically reset for you soon. Once reset, message items in current context window will be cleared in the new window, but notes and history items will be persistent across windows."
)

type TokenBudgetDefaults struct {
	ReminderThresholdTokens         int
	ReminderMessageTemplate         string
	GuidanceMessage                 string
	AutoCompactFallbackPrompt       string
	AutoCompactFallbackBufferTokens int
}

type TokenBudgetConfig struct {
	Enabled                         bool
	UseHistoryNotesExtension        bool
	ReminderThresholdTokens         *int
	ReminderMessageTemplate         string
	GuidanceMessage                 string
	AutoCompactFallbackPrompt       string
	AutoCompactFallbackBufferTokens *int
}

func (c *Config) TokenBudgetConfig() (*TokenBudgetConfig, error) {
	return c.TokenBudgetConfigWithDefaults(nil)
}

func (c *Config) TokenBudgetConfigWithDefaults(defaults *TokenBudgetDefaults) (*TokenBudgetConfig, error) {
	out := &TokenBudgetConfig{ReminderMessageTemplate: DefaultTokenBudgetReminderMessageTemplate}
	if c == nil || c.Values == nil {
		return out, nil
	}
	features, _ := c.Values["features"].(map[string]any)
	raw := features["token_budget"]
	if enabled, ok := raw.(bool); ok {
		out.Enabled = enabled
		if enabled && defaults != nil {
			candidate := *out
			applyTokenBudgetDefaults(&candidate, defaults)
			if validateTokenBudgetConfig(&candidate) == nil {
				return &candidate, nil
			}
		}
		return out, nil
	}
	table, ok := raw.(map[string]any)
	if !ok {
		return out, nil
	}
	out.Enabled, _ = table["enabled"].(bool)
	hasExplicitSettings := false
	for key := range table {
		// Rust #39830: use_history_notes_extension is a gate, not an explicit
		// token-budget setting.
		if key != "enabled" && key != "use_history_notes_extension" {
			hasExplicitSettings = true
			break
		}
	}
	if value, ok := table["use_history_notes_extension"].(bool); ok {
		out.UseHistoryNotesExtension = value
	}
	if out.Enabled && !hasExplicitSettings && defaults != nil {
		candidate := *out
		applyTokenBudgetDefaults(&candidate, defaults)
		if err := validateTokenBudgetConfig(&candidate); err == nil {
			return &candidate, nil
		}
	}
	if rawThreshold, exists := table["reminder_threshold_tokens"]; exists {
		value, ok := configPositiveInt(rawThreshold)
		if !ok {
			return nil, fmt.Errorf("features.token_budget.reminder_threshold_tokens must be positive")
		}
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
	if rawBuffer, exists := table["auto_compact_fallback_buffer_tokens"]; exists {
		value, ok := configPositiveInt(rawBuffer)
		if !ok {
			return nil, fmt.Errorf("features.token_budget.auto_compact_fallback_buffer_tokens must be positive")
		}
		out.AutoCompactFallbackBufferTokens = &value
	}
	if err := validateTokenBudgetConfig(out); err != nil {
		return nil, err
	}
	return out, nil
}

func applyTokenBudgetDefaults(out *TokenBudgetConfig, defaults *TokenBudgetDefaults) {
	if out == nil || defaults == nil {
		return
	}
	threshold := defaults.ReminderThresholdTokens
	buffer := defaults.AutoCompactFallbackBufferTokens
	out.ReminderThresholdTokens = &threshold
	out.ReminderMessageTemplate = strings.TrimSpace(defaults.ReminderMessageTemplate)
	out.GuidanceMessage = strings.TrimSpace(defaults.GuidanceMessage)
	out.AutoCompactFallbackPrompt = strings.TrimSpace(defaults.AutoCompactFallbackPrompt)
	out.AutoCompactFallbackBufferTokens = &buffer
}

func validateTokenBudgetConfig(value *TokenBudgetConfig) error {
	if value == nil {
		return nil
	}
	if value.ReminderThresholdTokens != nil && *value.ReminderThresholdTokens <= 0 {
		return fmt.Errorf("features.token_budget.reminder_threshold_tokens must be positive")
	}
	if strings.TrimSpace(value.ReminderMessageTemplate) == "" {
		return fmt.Errorf("features.token_budget.reminder_message_template must not be empty")
	}
	if len(value.ReminderMessageTemplate) > TokenBudgetReminderMessageTemplateMaxBytes {
		return fmt.Errorf("features.token_budget.reminder_message_template must not exceed %d bytes", TokenBudgetReminderMessageTemplateMaxBytes)
	}
	if len(value.GuidanceMessage) > TokenBudgetGuidanceMessageMaxBytes {
		return fmt.Errorf("features.token_budget.guidance_message must not exceed %d bytes", TokenBudgetGuidanceMessageMaxBytes)
	}
	if len(value.AutoCompactFallbackPrompt) > AutoCompactFallbackPromptMaxBytes {
		return fmt.Errorf("features.token_budget.auto_compact_fallback_prompt must not exceed %d bytes", AutoCompactFallbackPromptMaxBytes)
	}
	if value.AutoCompactFallbackPrompt != "" && value.AutoCompactFallbackBufferTokens == nil {
		return fmt.Errorf("features.token_budget.auto_compact_fallback_buffer_tokens is required when auto_compact_fallback_prompt is set")
	}
	if value.AutoCompactFallbackBufferTokens != nil && *value.AutoCompactFallbackBufferTokens <= 0 {
		return fmt.Errorf("features.token_budget.auto_compact_fallback_buffer_tokens must be positive")
	}
	return nil
}
