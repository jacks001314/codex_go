package config

import (
	"strings"
	"testing"
)

func TestTokenBudgetConfigParsesFallback(t *testing.T) {
	cfg := &Config{Values: map[string]any{"features": map[string]any{"token_budget": map[string]any{
		"enabled": true, "auto_compact_fallback_prompt": "  Write notes.  ", "auto_compact_fallback_buffer_tokens": int64(8000),
	}}}}
	got, err := cfg.TokenBudgetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.AutoCompactFallbackPrompt != "Write notes." || got.AutoCompactFallbackBufferTokens == nil || *got.AutoCompactFallbackBufferTokens != 8000 {
		t.Fatalf("config = %+v", got)
	}
}

func TestTokenBudgetConfigParsesUseHistoryNotesExtensionLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{"features": map[string]any{"token_budget": map[string]any{
		"enabled": true, "use_history_notes_extension": true,
	}}}}
	got, err := cfg.TokenBudgetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || !got.UseHistoryNotesExtension {
		t.Fatalf("config = %+v, want enabled + use_history_notes_extension", got)
	}

	// The gate alone does not make the token budget explicitly configured, so
	// model defaults still apply (Rust has_explicit_settings).
	withDefaults, err := cfg.TokenBudgetConfigWithDefaults(&TokenBudgetDefaults{
		ReminderThresholdTokens:         12000,
		ReminderMessageTemplate:         "model reminder",
		GuidanceMessage:                 "model guidance",
		AutoCompactFallbackPrompt:       "model fallback",
		AutoCompactFallbackBufferTokens: 8000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if withDefaults.ReminderThresholdTokens == nil || *withDefaults.ReminderThresholdTokens != 12000 {
		t.Fatalf("gate suppressed model defaults: %+v", withDefaults)
	}
	if !withDefaults.UseHistoryNotesExtension {
		t.Fatalf("gate lost after defaults: %+v", withDefaults)
	}
}

func TestTokenBudgetConfigRejectsInvalidFallback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table map[string]any
		want  string
	}{
		{"missing buffer", map[string]any{"enabled": true, "auto_compact_fallback_prompt": "notes"}, "is required"},
		{"zero buffer", map[string]any{"enabled": true, "auto_compact_fallback_prompt": "notes", "auto_compact_fallback_buffer_tokens": int64(0)}, "must be positive"},
		{"long prompt", map[string]any{"enabled": true, "auto_compact_fallback_prompt": strings.Repeat("x", 2001), "auto_compact_fallback_buffer_tokens": int64(1)}, "must not exceed 2000 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Values: map[string]any{"features": map[string]any{"token_budget": tc.table}}}
			_, err := cfg.TokenBudgetConfig()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestTokenBudgetConfigAppliesValidModelDefaults(t *testing.T) {
	cfg := &Config{Values: map[string]any{"features": map[string]any{"token_budget": map[string]any{"enabled": true}}}}
	got, err := cfg.TokenBudgetConfigWithDefaults(&TokenBudgetDefaults{
		ReminderThresholdTokens:         12000,
		ReminderMessageTemplate:         "remaining: {n_remaining}",
		GuidanceMessage:                 "keep notes",
		AutoCompactFallbackPrompt:       "summarize",
		AutoCompactFallbackBufferTokens: 8000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.ReminderThresholdTokens == nil || *got.ReminderThresholdTokens != 12000 || got.GuidanceMessage != "keep notes" || got.AutoCompactFallbackBufferTokens == nil || *got.AutoCompactFallbackBufferTokens != 8000 {
		t.Fatalf("config = %+v", got)
	}
}

func TestTokenBudgetConfigExplicitSettingsOverrideModelDefaults(t *testing.T) {
	cfg := &Config{Values: map[string]any{"features": map[string]any{"token_budget": map[string]any{
		"enabled": true, "guidance_message": "user guidance",
	}}}}
	got, err := cfg.TokenBudgetConfigWithDefaults(&TokenBudgetDefaults{
		ReminderThresholdTokens:         12000,
		ReminderMessageTemplate:         "model reminder",
		GuidanceMessage:                 "model guidance",
		AutoCompactFallbackPrompt:       "model fallback",
		AutoCompactFallbackBufferTokens: 8000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.GuidanceMessage != "user guidance" || got.ReminderThresholdTokens != nil || got.ReminderMessageTemplate != DefaultTokenBudgetReminderMessageTemplate || got.AutoCompactFallbackPrompt != "" {
		t.Fatalf("config = %+v", got)
	}
}

func TestTokenBudgetConfigIgnoresInvalidModelDefaults(t *testing.T) {
	cfg := &Config{Values: map[string]any{"features": map[string]any{"token_budget": true}}}
	got, err := cfg.TokenBudgetConfigWithDefaults(&TokenBudgetDefaults{
		ReminderThresholdTokens:         0,
		ReminderMessageTemplate:         "",
		GuidanceMessage:                 strings.Repeat("x", 2001),
		AutoCompactFallbackPrompt:       "fallback",
		AutoCompactFallbackBufferTokens: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.ReminderThresholdTokens != nil || got.GuidanceMessage != "" || got.ReminderMessageTemplate != DefaultTokenBudgetReminderMessageTemplate {
		t.Fatalf("config = %+v", got)
	}
}

func TestTokenBudgetConfigRejectsInvalidExplicitReminderAndGuidance(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table map[string]any
		want  string
	}{
		{"zero threshold", map[string]any{"enabled": true, "reminder_threshold_tokens": 0}, "must be positive"},
		{"empty reminder", map[string]any{"enabled": true, "reminder_message_template": "  "}, "must not be empty"},
		{"long guidance", map[string]any{"enabled": true, "guidance_message": strings.Repeat("x", 2001)}, "must not exceed 2000 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Values: map[string]any{"features": map[string]any{"token_budget": tc.table}}}
			if _, err := cfg.TokenBudgetConfig(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
