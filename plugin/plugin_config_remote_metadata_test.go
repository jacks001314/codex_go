package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// Mirrors Rust config/src/types_tests.rs
// (plugin_activation_inherits_defaults_without_explicit_enablement, #54424): a
// `[plugins.<id>]` table that declares only remote-plugin metadata keeps
// `enabled = None`, so it inherits the activation default instead of acting as
// an override, and `allows_plugin` reads `enabled` alone. The three fields are
// storage only: at aff92080f2 `git grep remote_plugin_id|release_id|auto_provision
// codex-rs/` returns no reader outside the struct definition and the loader's
// explicit `None` literals.
func TestPluginConfigRemoteMetadataLikeRust(t *testing.T) {
	const body = `
["empty@market"]
["mcp@market".mcp_servers.example]
enabled = false
["approval@market".mcp_servers.example]
default_tools_approval_mode = "approve"
["enabled@market"]
enabled = true
["disabled@market"]
enabled = false
["remote@market"]
remote_plugin_id = "remote-plugin-123"
release_id = "plugin-release-123"
auto_provision = true
`
	omitted := true
	explicitIDs := []string{
		"missing@market", "empty@market", "mcp@market", "approval@market",
		"enabled@market", "disabled@market", "remote@market",
	}
	for _, test := range []struct {
		name    string
		header  string
		deflt   *bool
		want    []bool
		wantHas bool
	}{
		{
			name:    "omitted default",
			want:    []bool{true, true, true, true, true, true, true},
			wantHas: false,
		},
		{
			name:    "default disabled",
			header:  "[_default]\nenabled = false\n",
			deflt:   new(bool),
			want:    []bool{false, false, false, false, true, false, false},
			wantHas: true,
		},
		{
			name:    "default enabled",
			header:  "[_default]\nenabled = true\n",
			deflt:   &omitted,
			want:    []bool{true, true, true, true, true, false, true},
			wantHas: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]any{}
			if err := toml.Unmarshal([]byte(test.header+body), &values); err != nil {
				t.Fatalf("toml.Unmarshal: %v", err)
			}
			policy := PluginsConfigTomlFromValues(map[string]any{"plugins": values})
			if test.wantHas {
				if policy.Default == nil || policy.Default.Enabled != *test.deflt {
					t.Fatalf("default = %#v, want enabled=%v", policy.Default, *test.deflt)
				}
			} else if policy.Default != nil {
				t.Fatalf("default = %#v, want an omitted `_default`", policy.Default)
			}

			// Rust asserts `config.plugins[id].enabled == None` for every entry
			// that declares no override, `remote@market` included.
			for _, id := range []string{"empty@market", "mcp@market", "approval@market", "remote@market"} {
				if enabled, ok := policy.ExplicitEnabled(id); ok {
					t.Fatalf("%s: explicit enabled = %v, want an omitted setting", id, enabled)
				}
			}
			if enabled, ok := policy.ExplicitEnabled("enabled@market"); !ok || !enabled {
				t.Fatalf("enabled@market = (%v, %v), want an explicit true", enabled, ok)
			}
			if enabled, ok := policy.ExplicitEnabled("disabled@market"); !ok || enabled {
				t.Fatalf("disabled@market = (%v, %v), want an explicit false", enabled, ok)
			}

			// The metadata is stored and stays out of activation state.
			remote, ok := policy.PluginConfigFor("remote@market")
			if !ok {
				t.Fatal("remote@market must keep a configured entry")
			}
			if remote.Enabled != nil {
				t.Fatalf("remote@market enabled = %v, want an omitted setting", *remote.Enabled)
			}
			if remote.RemotePluginID == nil || *remote.RemotePluginID != "remote-plugin-123" {
				t.Fatalf("remote_plugin_id = %v, want remote-plugin-123", remote.RemotePluginID)
			}
			if remote.ReleaseID == nil || *remote.ReleaseID != "plugin-release-123" {
				t.Fatalf("release_id = %v, want plugin-release-123", remote.ReleaseID)
			}
			if remote.AutoProvision == nil || !*remote.AutoProvision {
				t.Fatalf("auto_provision = %v, want true", remote.AutoProvision)
			}
			for _, id := range []string{"empty@market", "mcp@market"} {
				entry, ok := policy.PluginConfigFor(id)
				if !ok || entry != (PluginConfig{}) {
					t.Fatalf("%s = (%#v, %v), want an entry with no field set", id, entry, ok)
				}
			}

			for index, id := range explicitIDs {
				if got := policy.AllowsPlugin(id); got != test.want[index] {
					t.Fatalf("allows_plugin(%s) = %v, want %v", id, got, test.want[index])
				}
			}
		})
	}
}

// The metadata survives a full TOML round trip, like Rust's
// `toml::to_string` -> `toml::from_str` comparison in the same test.
func TestPluginConfigRemoteMetadataSurvivesTomlRoundTripLikeRust(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ConfigTOMLFilename)
	body := "" +
		"[plugins._default]\nenabled = false\n\n" +
		"[plugins.\"remote@market\"]\n" +
		"remote_plugin_id = \"remote-plugin-123\"\n" +
		"release_id = \"plugin-release-123\"\n" +
		"auto_provision = true\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	values, err := readMarketplaceConfig(configPath)
	if err != nil {
		t.Fatalf("readMarketplaceConfig: %v", err)
	}
	if err := writeMarketplaceConfig(configPath, values); err != nil {
		t.Fatalf("writeMarketplaceConfig: %v", err)
	}
	reloaded, err := readMarketplaceConfig(configPath)
	if err != nil {
		t.Fatalf("readMarketplaceConfig(reload): %v", err)
	}
	policy := PluginsConfigTomlFromValues(reloaded)
	remote, ok := policy.PluginConfigFor("remote@market")
	if !ok || remote.RemotePluginID == nil || *remote.RemotePluginID != "remote-plugin-123" ||
		remote.ReleaseID == nil || *remote.ReleaseID != "plugin-release-123" ||
		remote.AutoProvision == nil || !*remote.AutoProvision {
		t.Fatalf("round trip lost the remote metadata: %#v", remote)
	}
	if enabled, ok := policy.ExplicitEnabled("remote@market"); ok {
		t.Fatalf("the round trip invented an override: %v", enabled)
	}
	if policy.AllowsPlugin("remote@market") {
		t.Fatal("a disabled default must still narrow the metadata-only entry after a round trip")
	}
}

// #52220 / #54424 end to end over the real loader path: a marketplace manifest
// plus the configured table, read back through the public plugin list.
func TestPluginActivationPolicyThroughRealLoaderLikeRust(t *testing.T) {
	root := t.TempDir()
	manifestDir := filepath.Join(root, ".agents", "plugins")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	marketplace := `{"name":"acme","plugins":[{"name":"tool","source":{"source":"local","path":"./plugins/tool"}},{"name":"pinned","source":{"source":"local","path":"./plugins/pinned"}}]}`
	if err := os.WriteFile(filepath.Join(manifestDir, "marketplace.json"), []byte(marketplace), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tool", "pinned"} {
		pluginRoot := filepath.Join(root, "plugins", name, ".codex-plugin")
		if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		manifest := fmt.Sprintf(`{"name":%q,"description":"loader probe"}`, name)
		if err := os.WriteFile(filepath.Join(pluginRoot, "plugin.json"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name       string
		configBody string
		wantTool   bool
	}{
		{
			// Rust core-plugins/src/loader_tests.rs:635-638: a table that sets
			// only remote metadata is active under the default policy.
			name:       "remote metadata only keeps the default policy",
			configBody: "[plugins.\"tool@acme\"]\nremote_plugin_id = \"remote-plugin-123\"\nrelease_id = \"plugin-release-123\"\nauto_provision = true\n",
			wantTool:   true,
		},
		{
			// Rust core-plugins/src/loader_tests.rs:640-642: the same table under
			// `_default.enabled = false` is not active.
			name:       "a disabled default narrows the remote metadata entry",
			configBody: "[plugins._default]\nenabled = false\n\n[plugins.\"tool@acme\"]\nremote_plugin_id = \"remote-plugin-123\"\nrelease_id = \"plugin-release-123\"\nauto_provision = true\n",
			wantTool:   false,
		},
		{
			name:       "an explicit override survives a disabled default",
			configBody: "[plugins._default]\nenabled = false\n\n[plugins.\"tool@acme\"]\nenabled = true\n",
			wantTool:   true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), ConfigTOMLFilename)
			body := fmt.Sprintf("[marketplaces.acme]\nsource_type = \"local\"\nsource = %q\n\n%s", root, test.configBody)
			if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			service := NewPluginService()
			if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "acme", Source: root}); err != nil {
				t.Fatalf("AddMarketplace: %v", err)
			}
			service.SetMarketplaceConfigPath(configPath)

			listed := map[string]PluginSummary{}
			for _, summary := range service.List(&PluginListParams{IncludeInstalled: true}).Plugins {
				listed[summary.ID] = summary
			}
			if _, ok := listed[PluginsDefaultConfigKey]; ok {
				t.Fatalf("`_default` must never be listed as a plugin: %#v", listed)
			}
			tool, ok := listed["tool@acme"]
			if !ok {
				t.Fatalf("tool@acme missing from plugin/list: %#v", listed)
			}
			if tool.Enabled != test.wantTool {
				t.Fatalf("tool@acme enabled = %v, want %v", tool.Enabled, test.wantTool)
			}
			// `pinned@acme` exists only in the marketplace manifest, so it has no
			// configured entry: the merge layer can only clear the flag, and the
			// real loader leaves it unset.
			pinned, ok := listed["pinned@acme"]
			if !ok {
				t.Fatalf("pinned@acme missing from plugin/list: %#v", listed)
			}
			if pinned.Enabled {
				t.Fatalf("a discover-only plugin must not be activated: %#v", pinned)
			}
			if pinned.Installed {
				t.Fatalf("a discover-only plugin must not report installed: %#v", pinned)
			}
		})
	}
}
