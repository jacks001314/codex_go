package config

import "testing"

// TestLegacyFastModeDenialDisablesUltrafastLikeRust mirrors Rust #51253
// `legacy_speed_denial_is_normalized_without_changing_other_fragments`: only the
// legacy `rbac-fast-mode` fragment that denied Fast mode also denies Ultra Fast.
func TestLegacyFastModeDenialDisablesUltrafastLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		contents string
		want     *bool
	}{
		{name: "legacy denial", id: "rbac-fast-mode", contents: "[features]\nfast_mode = false", want: boolPointer(false)},
		{name: "legacy allow", id: "rbac-fast-mode", contents: "[features]\nfast_mode = true"},
		{name: "other source", id: "admin-layer", contents: "[features]\nfast_mode = false"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			requirements, err := applyCloudConfigBundle(map[string]any{}, nil, CloudConfigBundle{
				RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
					ID:       testCase.id,
					Name:     testCase.id,
					Contents: testCase.contents,
				}}},
			}, t.TempDir())
			if err != nil {
				t.Fatalf("applyCloudConfigBundle() error = %v", err)
			}
			value, present := requirements.FeatureRequirements["ultrafast_mode"]
			switch {
			case testCase.want == nil && present:
				t.Fatalf("ultrafast_mode = %v, want absent", value)
			case testCase.want != nil && (!present || value != *testCase.want):
				t.Fatalf("ultrafast_mode = %v (present=%v), want %v", value, present, *testCase.want)
			}
		})
	}
}

// TestEnterpriseSpeedOverrideBeatsLegacyBaselineLikeRust mirrors Rust #51253
// `enterprise_speed_override_still_takes_precedence_over_legacy_baseline`.
func TestEnterpriseSpeedOverrideBeatsLegacyBaselineLikeRust(t *testing.T) {
	requirements, err := applyCloudConfigBundle(map[string]any{}, nil, CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{
			{ID: "admin-layer", Name: "Admin", Contents: "[features]\nultrafast_mode = true"},
			{ID: "rbac-fast-mode", Name: "Legacy RBAC", Contents: "[features]\nfast_mode = false"},
		}},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("applyCloudConfigBundle() error = %v", err)
	}
	features := requirements.FeatureRequirements
	if features["fast_mode"] {
		t.Fatalf("fast_mode = %v, want false", features["fast_mode"])
	}
	if !features["ultrafast_mode"] {
		t.Fatalf("ultrafast_mode = %v, want true from the higher-priority enterprise layer", features["ultrafast_mode"])
	}
}

func boolPointer(value bool) *bool {
	return &value
}
