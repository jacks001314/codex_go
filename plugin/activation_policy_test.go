package plugin

import (
	"reflect"
	"strings"
	"testing"
)

// Mirrors Rust config/src/types_tests.rs
// (plugin_activation_inherits_defaults_without_explicit_enablement, #52220):
// without `_default` every plugin keeps legacy source and account enablement;
// otherwise an explicit setting overrides the configured default and every
// other plugin inherits it, including plugins absent from the table.
func TestPluginsConfigTomlAllowsPluginLikeRust(t *testing.T) {
	explicit := func(value bool) *bool { return &value }
	pluginIDs := []string{
		"missing@market",
		"empty@market",
		"mcp@market",
		"approval@market",
		"enabled@market",
		"disabled@market",
	}
	newPolicy := func(deflt *PluginsDefaultConfig) PluginsConfigToml {
		return PluginsConfigToml{
			Default: deflt,
			Plugins: map[string]*bool{
				"empty@market":    nil,
				"mcp@market":      nil,
				"approval@market": nil,
				"enabled@market":  explicit(true),
				"disabled@market": explicit(false),
			},
		}
	}
	for _, test := range []struct {
		name  string
		deflt *PluginsDefaultConfig
		want  []bool
	}{
		{"omitted default", nil, []bool{true, true, true, true, true, true}},
		{"default disabled", &PluginsDefaultConfig{Enabled: false}, []bool{false, false, false, false, true, false}},
		{"default enabled", &PluginsDefaultConfig{Enabled: true}, []bool{true, true, true, true, true, false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := newPolicy(test.deflt)
			got := make([]bool, 0, len(pluginIDs))
			for _, id := range pluginIDs {
				got = append(got, policy.AllowsPlugin(id))
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("allows_plugin = %v, want %v", got, test.want)
			}
		})
	}
}

// The reserved `_default` table is an activation default, never a plugin id
// (Rust core-plugins/src/marketplace_policy.rs:238-243 plus ParsePluginId).
func TestPluginsConfigTomlFromValuesKeepsDefaultOutOfPluginIDs(t *testing.T) {
	policy := PluginsConfigTomlFromValues(map[string]any{"plugins": map[string]any{
		"_default":    map[string]any{"enabled": false},
		"github@acme": map[string]any{"enabled": true},
		"slack@acme":  map[string]any{},
	}})
	if policy.Default == nil || policy.Default.Enabled {
		t.Fatalf("default = %#v, want a disabled default", policy.Default)
	}
	if _, ok := policy.Plugins["_default"]; ok {
		t.Fatalf("`_default` must not become a plugin entry: %#v", policy.Plugins)
	}
	if enabled, ok := policy.ExplicitEnabled("github@acme"); !ok || !enabled {
		t.Fatalf("github override = (%v, %v)", enabled, ok)
	}
	if _, ok := policy.ExplicitEnabled("slack@acme"); ok {
		t.Fatal("an omitted setting must not report an explicit override")
	}
	if policy.AllowsPlugin("slack@acme") {
		t.Fatal("an omitted setting must inherit the disabled default")
	}
	if !policy.AllowsPlugin("github@acme") {
		t.Fatal("an explicit enablement must override the default")
	}
}

// A `[plugins._default]` table that omits `enabled` still counts as declared
// and uses the Rust `default_enabled` value (config/src/types.rs:1079).
func TestPluginsConfigTomlDefaultWithoutEnabledFieldLikeRust(t *testing.T) {
	policy := PluginsConfigTomlFromValues(map[string]any{"plugins": map[string]any{
		"_default": map[string]any{},
	}})
	if policy.Default == nil || !policy.Default.Enabled {
		t.Fatalf("default = %#v, want an enabled default", policy.Default)
	}
	if !policy.AllowsPlugin("anything@acme") {
		t.Fatal("an enabled default must allow an unlisted plugin")
	}
}

// `plugins._default` is the activation default, never a plugin id (#52220).
// The plain forms (`plugins._default.enabled` and `plugins._default`) were only
// dropped incidentally because ParsePluginId rejects a bare `_default`; a
// `_default` entry that also carries a marketplace normalizes to
// `_default@<marketplace>`, which IS a valid PluginId, so the reserved key
// leaked into the toggle list. Rust keeps `_default` out of the per-plugin
// iteration explicitly (core-plugins/src/marketplace_policy.rs:238-243).
func TestCollectPluginEnabledCandidatesIgnoresDefaultActivationPolicyLikeRust(t *testing.T) {
	fullMap := map[string]any{
		"_default":    map[string]any{"enabled": false, "marketplace": "acme"},
		"github@acme": map[string]any{"enabled": true},
	}
	direct := []ConfigEdit{{Key: "plugins._default.enabled", Value: false}}
	table := []ConfigEdit{{Key: "plugins._default", Value: map[string]any{"enabled": false, "marketplace": "acme"}}}
	full := []ConfigEdit{{Key: "plugins", Value: fullMap}}
	for _, edits := range [][]ConfigEdit{direct, table, full} {
		for _, toggle := range CollectPluginEnabledCandidates(edits) {
			if strings.HasPrefix(toggle.PluginID.Key(), PluginsDefaultConfigKey) {
				t.Fatalf("`_default` surfaced as a plugin id: %#v", toggle)
			}
		}
	}
	if toggles := CollectPluginEnabledCandidates(table); len(toggles) != 0 {
		t.Fatalf("the `plugins._default` table alone must yield no toggles: %#v", toggles)
	}
	toggles := CollectPluginEnabledCandidates(full)
	if len(toggles) != 1 || toggles[0].PluginID.Key() != "github@acme" || !toggles[0].Enabled {
		t.Fatalf("toggles = %#v", toggles)
	}
}
