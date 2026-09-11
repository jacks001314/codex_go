package config

import "testing"

func profileName(value string) *string { return &value }

// TestHasLaunchSettingLikeRust mirrors #44693's has_launch_setting: a key is an
// explicit launch choice only when a `-c key=value` override supplies it or the
// highest-precedence layer that supplies it is the profiled user layer.
func TestHasLaunchSettingLikeRust(t *testing.T) {
	userProfile := Layer{
		Name:   LayerSource{Type: LayerSourceUser, Profile: profileName("work")},
		Config: map[string]any{"model": "gpt-5-user-profile"},
	}
	userPlain := Layer{Name: LayerSource{Type: LayerSourceUser}, Config: map[string]any{"model": "gpt-5-user"}}
	project := Layer{Name: LayerSource{Type: LayerSourceProject}, Config: map[string]any{"model": "gpt-5-project"}}
	managed := Layer{Name: LayerSource{Type: LayerSourceLegacyManagedConfigFromFile}, Config: map[string]any{"model": "gpt-5-managed"}}

	cases := []struct {
		name    string
		layers  []Layer
		cliKeys []string
		key     string
		want    bool
	}{
		{name: "cli override", layers: nil, cliKeys: []string{"model_reasoning_effort"}, key: "model_reasoning_effort", want: true},
		{name: "cli override for another key", layers: []Layer{userProfile}, cliKeys: []string{"model", "service_tier"}, key: "service_tier", want: true},
		{name: "profiled user layer", layers: []Layer{userProfile}, key: "model", want: true},
		{name: "unprofiled user layer", layers: []Layer{userPlain}, key: "model", want: false},
		{name: "key absent", layers: []Layer{userProfile}, key: "service_tier", want: false},
		{
			name:   "project shadows profile",
			layers: []Layer{userProfile, project},
			key:    "model",
			want:   false,
		},
		{
			name:   "managed shadows profile",
			layers: []Layer{userProfile, managed},
			key:    "model",
			want:   false,
		},
		{
			name:   "plain user layer shadows profile",
			layers: []Layer{userProfile, userPlain},
			key:    "model",
			want:   false,
		},
		{
			name:   "profile unrelated setting does not opt out",
			layers: []Layer{{Name: LayerSource{Type: LayerSourceUser, Profile: profileName("work")}, Config: map[string]any{"approval_policy": "never"}}},
			key:    "model",
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasLaunchSetting(tc.layers, tc.cliKeys, tc.key); got != tc.want {
				t.Fatalf("HasLaunchSetting() = %v, want %v", got, tc.want)
			}
		})
	}
}
