package app

import (
	"strings"
	"testing"

	"codex_go/cli"
)

func TestDaemonRestartFeatureOverridesLikeRust(t *testing.T) {
	features, err := daemonRestartFeatureOverrides(&cli.RootOptions{
		EnableFeatures:  []string{"api_key_model_discovery"},
		DisableFeatures: []string{"connectors"},
		ConfigOverrides: []string{
			"features.network_proxy.enabled=false",
			"features.auth_elicitation.enabled=true",
			"features.imagegenext=true",
			"features.image_generation=false",
		},
	})
	if err != nil {
		t.Fatalf("daemonRestartFeatureOverrides() error = %v", err)
	}
	want := map[string]bool{
		"api_key_model_discovery": true,
		"apps":                    false,
		"network_proxy":           false,
		"auth_elicitation":        true,
		"image_generation":        false,
	}
	if len(features) != len(want) {
		t.Fatalf("features = %#v, want %#v", features, want)
	}
	for name, enabled := range want {
		if features[name] != enabled {
			t.Fatalf("features[%q] = %t, want %t (%#v)", name, features[name], enabled, features)
		}
	}

	if _, err := daemonRestartFeatureOverrides(&cli.RootOptions{EnableFeatures: []string{"tool_registry"}}); err == nil ||
		!strings.Contains(err.Error(), "daemon restart requires a boolean feature toggle: tool_registry") {
		t.Fatalf("unknown feature error = %v", err)
	}
	if _, err := daemonRestartFeatureOverrides(&cli.RootOptions{ConfigOverrides: []string{`features.apps="yes"`}}); err == nil ||
		!strings.Contains(err.Error(), "daemon restart requires a boolean enablement for features.apps") {
		t.Fatalf("non-boolean feature error = %v", err)
	}
}
