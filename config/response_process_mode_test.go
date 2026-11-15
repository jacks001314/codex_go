package config

import (
	"strings"
	"testing"
)

func TestResponseProcessModeDefaultsToStreaming(t *testing.T) {
	if got := (&Config{}).ResponseProcessMode(); got != ResponseProcessModeStreaming {
		t.Fatalf("ResponseProcessMode() = %q, want streaming", got)
	}
	cfg := &Config{Values: map[string]any{"responses": map[string]any{}}}
	if got := cfg.ResponseProcessMode(); got != ResponseProcessModeStreaming {
		t.Fatalf("ResponseProcessMode() = %q, want streaming", got)
	}
}

func TestResponseProcessModeResolvesAfterCompleteResponse(t *testing.T) {
	values := map[string]any{"responses": map[string]any{"process_mode": "after_complete_response"}}
	if err := ValidateConfigValues(values); err != nil {
		t.Fatalf("ValidateConfigValues() error = %v", err)
	}
	cfg := &Config{Values: values}
	if got := cfg.ResponseProcessMode(); got != ResponseProcessModeAfterCompleteResponse {
		t.Fatalf("ResponseProcessMode() = %q, want after_complete_response", got)
	}
}

func TestResponseProcessModeRejectsUnknownVariant(t *testing.T) {
	err := ValidateConfigValues(map[string]any{
		"responses": map[string]any{"process_mode": "after_everything"},
	})
	if err == nil || !strings.Contains(err.Error(), "responses.process_mode") {
		t.Fatalf("validation error = %v, want responses.process_mode", err)
	}
}
