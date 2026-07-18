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
