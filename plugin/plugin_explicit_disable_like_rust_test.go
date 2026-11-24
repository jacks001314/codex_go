package plugin

import "testing"

// Rust #63468: an explicit `enabled = false` disables a plugin even when
// `[plugins._default]` is absent, an unlisted plugin keeps legacy enablement,
// and the policy only narrows a listing so an installed remote plugin stays
// installed while it reports as disabled.
func TestExplicitPluginDisablementWithoutDefaultLikeRust(t *testing.T) {
	disabled := false
	policy := PluginsConfigToml{Plugins: map[string]PluginConfig{
		"slack@acme": {Enabled: &disabled},
	}}

	if policy.AllowsPlugin("slack@acme") {
		t.Fatal("explicit disablement must win without a global default")
	}
	if !policy.AllowsPlugin("github@acme") {
		t.Fatal("an unlisted plugin keeps legacy enablement")
	}

	details := []PluginDetail{{Summary: PluginSummary{ID: "slack@acme", Installed: true, Enabled: true}}}
	applyPluginActivationPolicy(details, policy)
	if details[0].Summary.Enabled {
		t.Fatal("policy did not clear the effective enablement")
	}
	if !details[0].Summary.Installed {
		t.Fatal("policy must keep the installation and its account settings")
	}
}
