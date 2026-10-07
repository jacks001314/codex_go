package tui

import (
	"reflect"
	"testing"

	"codex_go/config"
	"codex_go/model"
)

func serviceTierTestBool(value bool) *bool { return &value }

// TestServiceTierEnabledRoutesTiersToOwningPolicyLikeRust covers Rust #51253
// (codex_features::Features::service_tier_enabled): flex is never gated,
// ultrafast follows features.ultrafast_mode, and every other tier follows
// features.fast_mode — which is what lets one policy allow a mode while the
// other denies it. Rust counterpart: service_tier_enabled
// (codex-rs/features/src/lib.rs) as exercised by
// `service_tier_commands_and_saved_selection_respect_independent_speed_policy`.
func TestServiceTierEnabledRoutesTiersToOwningPolicyLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		settings map[string]bool
		tier     string
		want     bool
	}{
		{name: "fast tier denied by policy", settings: map[string]bool{"fast_mode": false}, tier: "priority", want: false},
		{name: "fast tier allowed", settings: map[string]bool{"fast_mode": true}, tier: "priority", want: true},
		{name: "fast mode defaults to enabled", settings: nil, tier: "priority", want: true},
		{name: "ultrafast tier denied by its policy", settings: map[string]bool{"ultrafast_mode": false}, tier: "ultrafast", want: false},
		{name: "ultrafast tier is independent of fast mode", settings: map[string]bool{"fast_mode": false, "ultrafast_mode": true}, tier: "ultrafast", want: true},
		{name: "ultrafast mode defaults to enabled", settings: nil, tier: "ultrafast", want: true},
		{name: "fast mode does not gate ultrafast", settings: map[string]bool{"fast_mode": false}, tier: "ultrafast", want: true},
		{name: "flex is never gated", settings: map[string]bool{"fast_mode": false, "ultrafast_mode": false}, tier: "flex", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ServiceTierEnabled(tc.settings, tc.tier); got != tc.want {
				t.Fatalf("ServiceTierEnabled(%v, %q) = %v, want %v", tc.settings, tc.tier, got, tc.want)
			}
		})
	}
}

// TestConstrainServerServiceTiersUsesSharedFastGateLikeRust covers Rust #51253's
// constrain_server_service_tiers (codex-rs/tui/src/service_tier_resolution.rs):
// the server's reported policies filter the model catalog's tiers, an absent
// supportsIndependentSpeedModes keeps Ultra Fast behind the shared Fast gate
// (older servers), and a default tier the policies deny is cleared. Rust
// counterpart: `config_requirements_read_exposes_independent_speed_policy`
// (codex-rs/app-server/tests/suite/v2/config_rpc.rs) for the four policy
// combinations plus the shared-gate comment in constrain_server_service_tiers.
func TestConstrainServerServiceTiersUsesSharedFastGateLikeRust(t *testing.T) {
	catalog := func() []model.ModelInfo {
		return []model.ModelInfo{{
			Slug:               "gpt-5.5",
			ServiceTiers:       []string{"priority", "ultrafast", "flex"},
			DefaultServiceTier: "ultrafast",
		}}
	}
	cases := []struct {
		name         string
		independent  *bool
		requirements map[string]bool
		wantTiers    []string
		wantDefault  string
	}{
		{
			name:         "no feature requirements leaves the catalog alone",
			requirements: nil,
			wantTiers:    []string{"priority", "ultrafast", "flex"},
			wantDefault:  "ultrafast",
		},
		{
			name:         "older server keeps ultrafast behind the shared fast gate",
			independent:  nil,
			requirements: map[string]bool{"fast_mode": true, "ultrafast_mode": true},
			wantTiers:    []string{"priority", "ultrafast", "flex"},
			wantDefault:  "ultrafast",
		},
		{
			name:         "older server denying fast denies ultrafast too",
			independent:  nil,
			requirements: map[string]bool{"fast_mode": false, "ultrafast_mode": true},
			wantTiers:    []string{"flex"},
			wantDefault:  "",
		},
		{
			name:         "independent server allows ultrafast while fast is denied",
			independent:  serviceTierTestBool(true),
			requirements: map[string]bool{"fast_mode": false, "ultrafast_mode": true},
			wantTiers:    []string{"ultrafast", "flex"},
			wantDefault:  "ultrafast",
		},
		{
			name:         "independent server allows fast while ultrafast is denied",
			independent:  serviceTierTestBool(true),
			requirements: map[string]bool{"fast_mode": true, "ultrafast_mode": false},
			wantTiers:    []string{"priority", "flex"},
			wantDefault:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := catalog()
			response := &config.ConfigRequirementsReadResponse{
				SupportsIndependentSpeedModes: tc.independent,
				Requirements:                  &config.ConfigRequirements{FeatureRequirements: tc.requirements},
			}
			got := ConstrainServerServiceTiers(original, response)
			if len(got) != 1 {
				t.Fatalf("constrained %d models, want 1", len(got))
			}
			if !reflect.DeepEqual(got[0].ServiceTiers, tc.wantTiers) {
				t.Fatalf("service tiers = %#v, want %#v", got[0].ServiceTiers, tc.wantTiers)
			}
			if got[0].DefaultServiceTier != tc.wantDefault {
				t.Fatalf("default service tier = %q, want %q", got[0].DefaultServiceTier, tc.wantDefault)
			}
			// The constraint must not mutate the caller's catalog.
			if !reflect.DeepEqual(original, catalog()) {
				t.Fatalf("constraining mutated the source catalog: %#v", original)
			}
		})
	}
}
