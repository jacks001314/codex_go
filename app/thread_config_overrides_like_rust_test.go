package app

import (
	"testing"

	"codex_go/cli"
)

// TestRemoteThreadConfigForwardsSuppressUnstableFeaturesWarningLikeRust mirrors
// Rust #46529's config_request_overrides_from_config change: the
// unstable-features warning switch now travels with the thread configuration
// overrides so the server suppresses the same warning the launch would have,
// alongside the audited feature toggles.
func TestRemoteThreadConfigForwardsSuppressUnstableFeaturesWarningLikeRust(t *testing.T) {
	root := &cli.RootOptions{ConfigOverrides: []string{
		"suppress_unstable_features_warning=true",
		"features.multi_agent_mode=true",
	}}
	values, err := remoteConfigValues(root, cli.SharedOptions{})
	if err != nil {
		t.Fatalf("remoteConfigValues: %v", err)
	}
	if values["suppress_unstable_features_warning"] != true {
		t.Fatalf("suppress_unstable_features_warning = %#v, want true", values["suppress_unstable_features_warning"])
	}
	features, ok := values["features"].(map[string]any)
	if !ok {
		t.Fatalf("features override = %#v, want a table", values["features"])
	}
	if features["multi_agent_mode"] != true {
		t.Fatalf("features.multi_agent_mode = %#v, want true", features["multi_agent_mode"])
	}

	// A launch without the switch forwards nothing for it.
	values, err = remoteConfigValues(&cli.RootOptions{}, cli.SharedOptions{})
	if err != nil {
		t.Fatalf("remoteConfigValues without overrides: %v", err)
	}
	if _, ok := values["suppress_unstable_features_warning"]; ok {
		t.Fatalf("unexpected forwarded switch = %#v", values["suppress_unstable_features_warning"])
	}
}
