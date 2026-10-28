package config

import "testing"

func TestMemoriesConsolidationServiceTierLikeRust(t *testing.T) {
	defaults := (*Config)(nil).Memories()
	if defaults.ConsolidationServiceTier != "default" {
		t.Fatalf("default consolidation tier = %q, want default", defaults.ConsolidationServiceTier)
	}
	cases := []struct {
		configured string
		want       string
	}{
		{configured: "fast", want: "priority"},
		{configured: "priority", want: "priority"},
		{configured: "flex", want: "flex"},
		{configured: "default", want: "default"},
		{configured: "custom", want: "custom"},
	}
	for _, tc := range cases {
		cfg := &Config{Values: map[string]any{"memories": map[string]any{
			"consolidation_service_tier": tc.configured,
		}}}
		if got := cfg.Memories().ConsolidationServiceTier; got != tc.want {
			t.Fatalf("consolidation_service_tier=%q resolved to %q, want %q", tc.configured, got, tc.want)
		}
	}
}
