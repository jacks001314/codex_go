package config

import "testing"

func TestCodeModeToolInputSchemaMaxBytesLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{"features": map[string]any{
		"code_mode": map[string]any{"tool_input_schema_max_bytes": int64(24_000)},
	}}}
	if got := cfg.CodeModeToolInputSchemaMaxBytes(); got != 24_000 {
		t.Fatalf("CodeModeToolInputSchemaMaxBytes() = %d, want 24000", got)
	}
	invalid := &Config{Values: map[string]any{"features": map[string]any{
		"code_mode": map[string]any{"tool_input_schema_max_bytes": "large"},
	}}}
	if got := invalid.CodeModeToolInputSchemaMaxBytes(); got != 0 {
		t.Fatalf("invalid budget = %d, want default marker 0", got)
	}
}
