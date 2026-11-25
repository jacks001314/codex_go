package config

import "testing"

func TestSamplingInterruptConfigDefaultsAndOverrides(t *testing.T) {
	defaults, err := (&Config{}).MultiAgentV2Config(0)
	if err != nil || defaults.InterruptAtItemBoundary {
		t.Fatalf("%+v / %v", defaults, err)
	}
	for _, enabled := range []bool{false, true} {
		cfg := &Config{Values: map[string]any{"features": map[string]any{"multi_agent_v2": map[string]any{"interrupt_at_item_boundary": enabled}}}}
		got, err := cfg.MultiAgentV2Config(0)
		if err != nil || got.InterruptAtItemBoundary != enabled {
			t.Fatalf("%+v / %v", got, err)
		}
	}
}
