package tui

import (
	"reflect"
	"testing"

	"codex_go/config"
)

// TestConstrainServerServiceTierOptionsLikeRust pins the picker-catalog form of
// Rust #51253's constrain_server_service_tiers
// (codex-rs/tui/src/service_tier_resolution.rs), which the TUI bootstrap applies
// to the presets it just built from model/list
// (codex-rs/tui/src/app_server_session.rs). The TUI's picker options carry the
// same tier ids, and both the picker and the service-tier commands derive from
// them, so constraining them is what makes the server's speed policies effective
// on the model-catalog path.
//
// Rust counterparts: `config_requirements_read_exposes_independent_speed_policy`
// (codex-rs/app-server/tests/suite/v2/config_rpc.rs) for the policy matrix and
// `service_tier_commands_and_saved_selection_respect_independent_speed_policy`
// (codex-rs/tui/src/chatwidget/tests/slash_commands.rs) for the shared-gate
// behavior an older server relies on.
func TestConstrainServerServiceTierOptionsLikeRust(t *testing.T) {
	catalog := func() []ModelPickerOption {
		return []ModelPickerOption{
			{ID: "gpt-5.5", Label: "gpt-5.5", ServiceTiers: []string{"priority", "ultrafast", "flex"}},
			{ID: "gpt-5.5-mini", Label: "mini", ServiceTiers: nil},
		}
	}
	cases := []struct {
		name         string
		independent  *bool
		requirements map[string]bool
		wantTiers    []string
	}{
		{
			name:         "no feature requirements leaves the catalog alone",
			requirements: nil,
			wantTiers:    []string{"priority", "ultrafast", "flex"},
		},
		{
			name:         "older server keeps ultrafast behind the shared fast gate",
			independent:  nil,
			requirements: map[string]bool{"fast_mode": true, "ultrafast_mode": true},
			wantTiers:    []string{"priority", "ultrafast", "flex"},
		},
		{
			name:         "older server denying fast denies ultrafast too",
			independent:  nil,
			requirements: map[string]bool{"fast_mode": false, "ultrafast_mode": true},
			wantTiers:    []string{"flex"},
		},
		{
			name:         "independent server allows ultrafast while fast is denied",
			independent:  serviceTierTestBool(true),
			requirements: map[string]bool{"fast_mode": false, "ultrafast_mode": true},
			wantTiers:    []string{"ultrafast", "flex"},
		},
		{
			name:         "independent server allows fast while ultrafast is denied",
			independent:  serviceTierTestBool(true),
			requirements: map[string]bool{"fast_mode": true, "ultrafast_mode": false},
			wantTiers:    []string{"priority", "flex"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := catalog()
			var response *config.ConfigRequirementsReadResponse
			if tc.requirements != nil {
				response = &config.ConfigRequirementsReadResponse{
					SupportsIndependentSpeedModes: tc.independent,
					Requirements:                  &config.ConfigRequirements{FeatureRequirements: tc.requirements},
				}
			}
			got := ConstrainServerServiceTierOptions(original, response)
			if len(got) != 2 {
				t.Fatalf("constrained %d options, want 2", len(got))
			}
			if !reflect.DeepEqual(got[0].ServiceTiers, tc.wantTiers) {
				t.Fatalf("service tiers = %#v, want %#v", got[0].ServiceTiers, tc.wantTiers)
			}
			// A model without tier metadata keeps its nil list.
			if got[1].ServiceTiers != nil {
				t.Fatalf("tier-less model gained tiers: %#v", got[1].ServiceTiers)
			}
			// The constraint must not mutate the caller's catalog.
			if !reflect.DeepEqual(original, catalog()) {
				t.Fatalf("constraining mutated the source catalog: %#v", original)
			}
		})
	}
}

// TestServiceTierPolicyFromRequirementsLikeRust pins the shared policy the
// catalog constraining and the service-tier command filtering both read
// (Rust #51253: constrain_server_service_tiers' `tier_enabled` closure). A
// response that carries no feature requirements reports ok=false so callers
// leave the catalog untouched, matching Rust's early return.
func TestServiceTierPolicyFromRequirementsLikeRust(t *testing.T) {
	if _, ok := ServiceTierPolicyFromRequirements(nil); ok {
		t.Fatal("nil response reported a usable policy")
	}
	if _, ok := ServiceTierPolicyFromRequirements(&config.ConfigRequirementsReadResponse{
		Requirements: &config.ConfigRequirements{},
	}); ok {
		t.Fatal("response without feature requirements reported a usable policy")
	}
	policy, ok := ServiceTierPolicyFromRequirements(&config.ConfigRequirementsReadResponse{
		SupportsIndependentSpeedModes: serviceTierTestBool(true),
		Requirements: &config.ConfigRequirements{
			FeatureRequirements: map[string]bool{"fast_mode": false, "ultrafast_mode": true},
		},
	})
	if !ok {
		t.Fatal("response with feature requirements reported no policy")
	}
	for tier, want := range map[string]bool{"priority": false, "ultrafast": true, "flex": true} {
		if got := policy(tier); got != want {
			t.Fatalf("policy(%q) = %v, want %v", tier, got, want)
		}
	}
}
