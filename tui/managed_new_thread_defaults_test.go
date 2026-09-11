package tui

import (
	"testing"

	"codex_go/config"
)

func profiledUserLayer(values map[string]any) config.Layer {
	profile := "work"
	return config.Layer{
		Name:   config.LayerSource{Type: config.LayerSourceUser, Profile: &profile},
		Config: values,
	}
}

// TestApplyManagedNewThreadDefaultsLikeRust mirrors Rust #44693: managed
// defaults fill model/effort only when the user did not explicitly choose
// model or reasoning effort, service tier is independent, and a profile only
// counts when it supplies the highest-precedence value.
func TestApplyManagedNewThreadDefaultsLikeRust(t *testing.T) {
	defaults := &ManagedNewThreadDefaults{Model: "gpt-5-managed", ReasoningEffort: "high", ServiceTier: "fast"}

	cases := []struct {
		name         string
		target       ManagedNewThreadDefaultsTarget
		layers       []config.Layer
		cli          []string
		harnessModel bool
		harnessTier  bool
		wantModel    string
		wantEffort   string
		wantTier     string
	}{
		{
			name:       "no explicit settings applies all managed values",
			wantModel:  "gpt-5-managed",
			wantEffort: "high",
			wantTier:   "priority",
		},
		{
			name:         "harness model opts out of model and effort",
			target:       ManagedNewThreadDefaultsTarget{Model: "gpt-5.4", ReasoningEffort: "low"},
			harnessModel: true,
			wantModel:    "gpt-5.4",
			wantEffort:   "low",
			wantTier:     "priority",
		},
		{
			name:       "cli effort opts out of the model pair",
			cli:        []string{"model_reasoning_effort"},
			target:     ManagedNewThreadDefaultsTarget{ReasoningEffort: "low"},
			wantModel:  "",
			wantEffort: "low",
			wantTier:   "priority",
		},
		{
			name:      "cli model opts out of the model pair",
			cli:       []string{"model"},
			target:    ManagedNewThreadDefaultsTarget{Model: "gpt-5.4"},
			wantModel: "gpt-5.4",
			wantTier:  "priority",
		},
		{
			name:      "profiled user layer supplies the active model",
			layers:    []config.Layer{profiledUserLayer(map[string]any{"model": "gpt-5-profile"})},
			target:    ManagedNewThreadDefaultsTarget{Model: "gpt-5-profile"},
			wantModel: "gpt-5-profile",
			wantTier:  "priority",
		},
		{
			name: "project layer shadows the profile",
			layers: []config.Layer{
				profiledUserLayer(map[string]any{"model": "gpt-5-profile"}),
				{Name: config.LayerSource{Type: config.LayerSourceProject}, Config: map[string]any{"model": "gpt-5-project"}},
			},
			target:     ManagedNewThreadDefaultsTarget{Model: "gpt-5-project"},
			wantModel:  "gpt-5-managed",
			wantEffort: "high",
			wantTier:   "priority",
		},
		{
			name:       "explicit service tier stays independent",
			cli:        []string{"service_tier"},
			target:     ManagedNewThreadDefaultsTarget{ServiceTier: "flex"},
			wantModel:  "gpt-5-managed",
			wantEffort: "high",
			wantTier:   "flex",
		},
		{
			name:        "harness service tier flag opts out of the managed tier only",
			harnessTier: true,
			target:      ManagedNewThreadDefaultsTarget{ServiceTier: "flex"},
			wantModel:   "gpt-5-managed",
			wantEffort:  "high",
			wantTier:    "flex",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.target
			ApplyManagedNewThreadDefaults(&target, defaults, tc.layers, tc.cli, tc.harnessModel, tc.harnessTier)
			if target.Model != tc.wantModel || target.ReasoningEffort != tc.wantEffort || target.ServiceTier != tc.wantTier {
				t.Fatalf("target = %#v, want model=%q effort=%q tier=%q", target, tc.wantModel, tc.wantEffort, tc.wantTier)
			}
		})
	}

	// No defaults (or no target) leaves the selection untouched.
	untouched := ManagedNewThreadDefaultsTarget{Model: "gpt-5.4"}
	ApplyManagedNewThreadDefaults(&untouched, nil, nil, nil, false, false)
	if untouched.Model != "gpt-5.4" {
		t.Fatalf("nil defaults changed target: %#v", untouched)
	}
	ApplyManagedNewThreadDefaults(nil, defaults, nil, nil, false, false)
}

func TestNormalizeManagedServiceTierLikeRust(t *testing.T) {
	cases := map[string]string{
		"fast":         "priority",
		"priority":     "priority",
		"flex":         "flex",
		"default":      "default",
		"unknown-tier": "unknown-tier",
	}
	for input, want := range cases {
		if got := NormalizeManagedServiceTier(input); got != want {
			t.Fatalf("NormalizeManagedServiceTier(%q) = %q, want %q", input, got, want)
		}
	}
}
