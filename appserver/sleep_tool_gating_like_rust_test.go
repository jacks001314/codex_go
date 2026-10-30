package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/turn"
)

// TestSleepToolModeGatingLikeRust covers the `sleep_tool` feature and the
// structured `sleep_tool.mode` override added by Rust #41243: always_on
// registers clock.sleep whenever the feature is enabled, while the default
// model_driven mode preserves the model capability and the legacy
// `current_time_reminder.sleep_tool` behavior.
func TestSleepToolModeGatingLikeRust(t *testing.T) {
	tests := []struct {
		name          string
		features      map[string]any
		modelHasClock bool
		wantCurrTime  bool
		wantSleep     bool
	}{
		{
			name:          "legacy clock config enables sleep",
			features:      map[string]any{"current_time_reminder": map[string]any{"enabled": true, "sleep_tool": true}},
			modelHasClock: false,
			wantCurrTime:  true,
			wantSleep:     true,
		},
		{
			name:          "legacy clock config leaves sleep off",
			features:      map[string]any{"current_time_reminder": true},
			modelHasClock: false,
			wantCurrTime:  true,
			wantSleep:     false,
		},
		{
			name:          "legacy clock config wins over model clock in model_driven mode",
			features:      map[string]any{"current_time_reminder": map[string]any{"enabled": true, "sleep_tool": false}},
			modelHasClock: true,
			wantCurrTime:  true,
			wantSleep:     false,
		},
		{
			name:          "always_on ignores legacy clock settings",
			features:      map[string]any{"current_time_reminder": false, "sleep_tool": map[string]any{"mode": "always_on"}},
			modelHasClock: false,
			wantCurrTime:  false,
			wantSleep:     true,
		},
		{
			name:          "always_on still requires the feature gate",
			features:      map[string]any{"sleep_tool": map[string]any{"enabled": false, "mode": "always_on"}},
			modelHasClock: true,
			wantCurrTime:  true,
			wantSleep:     false,
		},
		{
			name:          "model_driven without reminder feature follows the model",
			features:      map[string]any{"sleep_tool": map[string]any{"mode": "model_driven"}},
			modelHasClock: true,
			wantCurrTime:  true,
			wantSleep:     true,
		},
		{
			name:          "model_driven without reminder feature or model clock stays off",
			features:      map[string]any{"current_time_reminder": false},
			modelHasClock: false,
			wantCurrTime:  false,
			wantSleep:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Values: map[string]any{"features": tt.features}}
			gotCurrTime, gotSleep := decideClockToolEnablement(cfg, &turn.TurnStartParams{}, tt.modelHasClock)
			if gotCurrTime != tt.wantCurrTime {
				t.Fatalf("current-time = %v, want %v", gotCurrTime, tt.wantCurrTime)
			}
			if gotSleep != tt.wantSleep {
				t.Fatalf("sleep = %v, want %v", gotSleep, tt.wantSleep)
			}
		})
	}
}
