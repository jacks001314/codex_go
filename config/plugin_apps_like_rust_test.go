package config

import "testing"

// Mirrors Rust #58428 app_tool_policy_tests: app settings from enabled plugins
// apply connector-wide, earlier plugin names win conflicts, and explicit
// top-level app fields are applied last.
func TestPluginAppsOverlaysApplyConnectorWideLikeRust(t *testing.T) {
	values := map[string]any{
		"plugins": map[string]any{
			"slack@acme": map[string]any{
				"apps": map[string]any{
					"_default": map[string]any{"enabled": false},
					"slack":    map[string]any{"enabled": false, "approvals_reviewer": "slack"},
				},
			},
			"github@acme": map[string]any{
				"apps": map[string]any{
					"slack": map[string]any{"approvals_reviewer": "github"},
				},
			},
		},
		"apps": map[string]any{
			"slack": map[string]any{"enabled": true},
		},
	}
	if !mergePluginApps(values) {
		t.Fatal("mergePluginApps() found no overlays")
	}
	apps, ok := values["apps"].(map[string]any)
	if !ok {
		t.Fatalf("apps = %#v", values["apps"])
	}
	if _, ok := apps["_default"]; ok {
		t.Fatal("plugin _default leaked into connector settings")
	}
	slack, ok := apps["slack"].(map[string]any)
	if !ok {
		t.Fatalf("apps.slack = %#v", apps["slack"])
	}
	if slack["enabled"] != true {
		t.Fatalf("enabled = %#v, want the top-level value", slack["enabled"])
	}
	if slack["approvals_reviewer"] != "github" {
		t.Fatalf("approvals_reviewer = %#v, want the alphabetically earlier plugin name", slack["approvals_reviewer"])
	}
}

func TestPluginAppsOverlaysSkipDisabledPluginsLikeRust(t *testing.T) {
	values := map[string]any{
		"plugins": map[string]any{
			"_default": map[string]any{"enabled": false},
			"off@acme": map[string]any{
				"apps": map[string]any{"slack": map[string]any{"enabled": false}},
			},
			"on@acme": map[string]any{
				"enabled": true,
				"apps":    map[string]any{"github": map[string]any{"enabled": true}},
			},
		},
	}
	mergePluginApps(values)
	apps, _ := values["apps"].(map[string]any)
	if _, ok := apps["slack"]; ok {
		t.Fatal("a plugin disallowed by the default policy contributed settings")
	}
	if _, ok := apps["github"]; !ok {
		t.Fatal("an explicitly enabled plugin did not contribute settings")
	}
}

func TestPluginAppsOriginsProjectedLikeRust(t *testing.T) {
	values := map[string]any{
		"plugins": map[string]any{
			"b@acme": map[string]any{"apps": map[string]any{"slack": map[string]any{"enabled": false}}},
			"a@acme": map[string]any{"apps": map[string]any{"slack": map[string]any{"approvals_reviewer": "a"}}},
		},
		"apps": map[string]any{"slack": map[string]any{"enabled": true}},
	}
	pluginB := LayerMetadata{Name: LayerSource{Type: LayerSourceUser}, Version: "b"}
	pluginA := LayerMetadata{Name: LayerSource{Type: LayerSourceProject}, Version: "a"}
	topLevel := LayerMetadata{Name: LayerSource{Type: LayerSourceSystem}, Version: "t"}
	origins := map[string]LayerMetadata{
		"plugins.b@acme.apps.slack.enabled":            pluginB,
		"plugins.a@acme.apps.slack.approvals_reviewer": pluginA,
		"plugins.b@acme.apps._default.enabled":         pluginB,
		"apps.slack.enabled":                           topLevel,
	}
	mergePluginApps(values)
	projectPluginAppsOrigins(values, origins)
	if got := origins["apps.slack.enabled"]; got != topLevel {
		t.Fatalf("apps.slack.enabled origin = %#v, want the top-level layer", got)
	}
	if got := origins["apps.slack.approvals_reviewer"]; got != pluginA {
		t.Fatalf("apps.slack.approvals_reviewer origin = %#v, want plugin a", got)
	}
	if _, ok := origins["apps._default.enabled"]; ok {
		t.Fatal("plugin _default origin was projected")
	}
}
