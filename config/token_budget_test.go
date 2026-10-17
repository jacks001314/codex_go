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

func TestTokenBudgetConfigModelDefaultsActivateWithoutExplicitConfigLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{}}
	got, err := cfg.TokenBudgetConfigWithDefaults(&TokenBudgetDefaults{
		Enabled:                         true,
		UseHistoryNotesExtension:        true,
		ReminderThresholdTokens:         12000,
		ReminderMessageTemplate:         "remaining: {n_remaining}",
		GuidanceMessage:                 "keep notes",
		AutoCompactFallbackPrompt:       "summarize",
		AutoCompactFallbackBufferTokens: 8000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || !got.UseHistoryNotesExtension || got.ReminderThresholdTokens == nil ||
		*got.ReminderThresholdTokens != 12000 || got.GuidanceMessage != "keep notes" {
		t.Fatalf("config = %+v", got)
	}
}

func TestTokenBudgetConfigExplicitConfigDisablesModelDefaultActivationLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{"features": map[string]any{"token_budget": false}}}
	got, err := cfg.TokenBudgetConfigWithDefaults(&TokenBudgetDefaults{
		Enabled:                  true,
		UseHistoryNotesExtension: true,
		ReminderThresholdTokens:  12000,
		ReminderMessageTemplate:  "remaining: {n_remaining}",
		GuidanceMessage:          "keep notes",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.UseHistoryNotesExtension || got.ReminderThresholdTokens != nil || got.GuidanceMessage != "" {
		t.Fatalf("explicit disabled config was overridden: %+v", got)
	}
	if !cfg.HasExplicitTokenBudgetConfig() {
		t.Fatal("explicit token-budget config was not detected")
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

func TestHasExplicitTokenBudgetSettingsIgnoresActivationGatesLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table map[string]any
		want  bool
	}{
		{name: "missing", table: nil, want: false},
		{name: "enabled", table: map[string]any{"enabled": true}, want: false},
		{name: "history notes gate", table: map[string]any{"enabled": true, "use_history_notes_extension": true}, want: false},
		{name: "guidance", table: map[string]any{"guidance_message": "user guidance"}, want: true},
		{name: "threshold", table: map[string]any{"reminder_threshold_tokens": int64(12000)}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Values: map[string]any{"features": map[string]any{"token_budget": tc.table}}}
			if got := cfg.HasExplicitTokenBudgetSettings(); got != tc.want {
				t.Fatalf("HasExplicitTokenBudgetSettings() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveTokenBudgetConfigUsesEachModelsDefaultsAndPreservesPreferences(t *testing.T) {
	configured := &TokenBudgetConfig{
		Enabled:                  true,
		UseHistoryNotesExtension: true,
		ReminderThresholdTokens:  intPointer(500),
		ReminderMessageTemplate:  "user reminder",
		GuidanceMessage:          "user guidance",
	}
	modelDefaults := &TokenBudgetDefaults{
		ReminderThresholdTokens:         2000,
		ReminderMessageTemplate:         "model reminder",
		GuidanceMessage:                 "model guidance",
		AutoCompactFallbackPrompt:       "save state",
		AutoCompactFallbackBufferTokens: 4000,
	}
	resolved := ResolveTokenBudgetConfig(configured, true, modelDefaults, true)
	if resolved == configured || resolved.ReminderThresholdTokens == configured.ReminderThresholdTokens {
		t.Fatal("resolved config must not alias captured preferences")
	}
	if !resolved.Enabled || !resolved.UseHistoryNotesExtension ||
		resolved.ReminderThresholdTokens == nil || *resolved.ReminderThresholdTokens != 2000 ||
		resolved.ReminderMessageTemplate != "model reminder" ||
		resolved.GuidanceMessage != "model guidance" ||
		resolved.AutoCompactFallbackBufferTokens == nil || *resolved.AutoCompactFallbackBufferTokens != 4000 {
		t.Fatalf("resolved model defaults = %+v", resolved)
	}

	explicit := ResolveTokenBudgetConfig(configured, false, modelDefaults, true)
	if explicit.ReminderThresholdTokens == nil || *explicit.ReminderThresholdTokens != 500 ||
		explicit.ReminderMessageTemplate != "user reminder" ||
		explicit.GuidanceMessage != "user guidance" ||
		explicit.AutoCompactFallbackPrompt != "" {
		t.Fatalf("explicit preferences = %+v", explicit)
	}

	// Invalid model defaults are ignored and the captured preferences survive.
	invalid := ResolveTokenBudgetConfig(configured, true, &TokenBudgetDefaults{
		ReminderMessageTemplate: "",
	}, true)
	if invalid.ReminderThresholdTokens == nil || *invalid.ReminderThresholdTokens != 500 ||
		invalid.ReminderMessageTemplate != "user reminder" {
		t.Fatalf("invalid model defaults replaced preferences: %+v", invalid)
	}
}

func intPointer(value int) *int {
	return &value
}
