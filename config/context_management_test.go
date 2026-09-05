package config

import "testing"

func TestContextManagementConfigParsesExperimentalMode(t *testing.T) {
	cfg := &Config{Values: map[string]any{"features": map[string]any{
		"context_management": map[string]any{"experimental_mode": true},
	}}}
	if got := cfg.ContextManagementConfig(); !got.ExperimentalMode {
		t.Fatalf("ContextManagementConfig() = %#v", got)
	}
	if got := (&Config{Values: map[string]any{}}).ContextManagementConfig(); got.ExperimentalMode {
		t.Fatalf("absent context management should be disabled: %#v", got)
	}
}

func TestContextManagementConfigRejectsUnknownFields(t *testing.T) {
	if err := validateKnownFeatureFields(map[string]any{
		"context_management": map[string]any{"experimental_mode": true, "unknown": true},
	}); err == nil {
		t.Fatal("unknown context_management field accepted")
	}
}
