package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigMarketplacesFromValues(t *testing.T) {
	svc := NewPluginService()
	marketRoot := t.TempDir()
	manifest := `{"name":"acme","plugins":[{"name":"acme-tool","source":{"source":"local","path":"./plugins/acme-tool"}}]}`
	manifestPath := filepath.Join(marketRoot, ".agents", "plugins", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	values := map[string]any{
		"marketplaces": map[string]any{
			"acme": map[string]any{"source_type": "local", "source": marketRoot},
		},
	}
	entries, loadErrors := svc.ResolveConfigMarketplaces(values)
	if len(loadErrors) != 0 {
		t.Fatalf("load errors = %#v", loadErrors)
	}
	if len(entries) != 1 || entries[0].Name != "acme" {
		t.Fatalf("config marketplaces = %#v", entries)
	}
	if len(entries[0].Plugins) != 1 || entries[0].Plugins[0].Name != "acme-tool" {
		t.Fatalf("config marketplace plugins = %#v", entries[0].Plugins)
	}
}

func TestResolveConfigMarketplacesEmptyConfig(t *testing.T) {
	svc := NewPluginService()
	if entries, loadErrors := svc.ResolveConfigMarketplaces(nil); entries != nil || loadErrors != nil {
		t.Fatalf("nil config should resolve empty: entries=%#v errors=%#v", entries, loadErrors)
	}
	if entries, loadErrors := svc.ResolveConfigMarketplaces(map[string]any{}); entries != nil || loadErrors != nil {
		t.Fatalf("empty config should resolve empty: entries=%#v errors=%#v", entries, loadErrors)
	}
	// A config that declares no marketplaces resolves empty too.
	if entries, _ := svc.ResolveConfigMarketplaces(map[string]any{"features": map[string]any{"plugins": true}}); entries != nil {
		t.Fatalf("no-marketplaces config should resolve empty: %#v", entries)
	}
}

// #52220: `plugins._default.enabled` supplies the activation default for an
// installed plugin whose entry omits `enabled`, an explicit setting overrides
// it, and the reserved key never becomes a plugin (Rust
// config/src/types.rs:1059-1072).
func TestConfiguredPluginsFromConfigInheritsActivationDefaultLikeRust(t *testing.T) {
	values := map[string]any{"plugins": map[string]any{
		"_default":      map[string]any{"enabled": false},
		"sample@acme":   map[string]any{"installed": true},
		"explicit@acme": map[string]any{"installed": true, "enabled": true},
	}}
	byID := map[string]PluginDetail{}
	details := configuredPluginsFromConfig(values, nil)
	for _, detail := range details {
		byID[detail.Summary.ID] = detail
	}
	if _, ok := byID[PluginsDefaultConfigKey]; ok {
		t.Fatalf("`_default` must not be reported as a plugin: %#v", details)
	}
	if len(details) != 2 {
		t.Fatalf("details = %#v", details)
	}
	if byID["sample@acme"].Summary.Enabled {
		t.Fatal("an omitted setting must inherit the disabled default")
	}
	if !byID["explicit@acme"].Summary.Enabled {
		t.Fatal("an explicit enablement must override the disabled default")
	}
}

// #52220: configuration can narrow an installation but never activate one its
// source disabled (Rust core-plugins/src/loader.rs:184-187).
func TestConfiguredPluginsFromConfigNeverWidensSourceEnablementLikeRust(t *testing.T) {
	values := map[string]any{"plugins": map[string]any{
		"_default":      map[string]any{"enabled": true},
		"disabled@acme": map[string]any{"installed": true, "enabled": false},
	}}
	details := configuredPluginsFromConfig(values, nil)
	if len(details) != 1 || details[0].Summary.Enabled {
		t.Fatalf("source-disabled plugin = %#v", details)
	}
	// Without `_default` the policy allows every plugin, so only the source
	// decides; a truthy policy vote must not flip a disabled source back on.
	plain := map[string]any{"plugins": map[string]any{
		"disabled@acme": map[string]any{"installed": true, "enabled": false},
	}}
	details = configuredPluginsFromConfig(plain, nil)
	if len(details) != 1 || details[0].Summary.Enabled {
		t.Fatalf("source-disabled plugin without _default = %#v", details)
	}
}

// #52220 / #54424 through the real loader path. The discovered details come from
// `marketplacePluginDetailFromManifest`, the constructor `loadMarketplacePlugins`
// itself calls, so they carry the real shape: `Enabled` unset and `Installed`
// false. `mergePluginDetails` can therefore only ever clear the flag, never set
// it, which is why the Rust loader applies the policy to `configured_plugins`
// alone (core-plugins/src/loader.rs:184-187).
func TestMergePluginDetailsAppliesActivationPolicyToDiscoveredPluginsLikeRust(t *testing.T) {
	explicitEnabled := true
	policy := PluginsConfigToml{
		Default: &PluginsDefaultConfig{Enabled: false},
		Plugins: map[string]PluginConfig{"explicit@acme": {Enabled: &explicitEnabled}},
	}
	pluginRoot := t.TempDir()
	discovered := []PluginDetail{
		marketplacePluginDetailFromManifest("tool", "acme", "", "", pluginRoot, marketplaceManifestPlugin{Name: "tool"}, nil),
		marketplacePluginDetailFromManifest("explicit", "acme", "", "", pluginRoot, marketplaceManifestPlugin{Name: "explicit"}, nil),
	}
	for _, detail := range discovered {
		if detail.Summary.Enabled || detail.Summary.Installed {
			t.Fatalf("the real loader must leave a discovered plugin inactive and uninstalled: %#v", detail.Summary)
		}
	}

	merged := mergePluginDetails(nil, discovered, policy)
	if len(merged) != 2 {
		t.Fatalf("merged = %#v", merged)
	}
	for _, detail := range merged {
		if detail.Summary.Enabled {
			t.Fatalf("mergePluginDetails must not activate a discovered plugin: %#v", detail.Summary)
		}
	}

	// Only the configured side carries activation, and there the merge keeps the
	// value `configuredPluginsFromConfig` resolved: an entry the disabled default
	// narrows stays inactive, and an explicit `enabled = true` survives.
	stored := []PluginDetail{
		{Summary: PluginSummary{ID: "tool@acme", Installed: true, Enabled: true}},
		{Summary: PluginSummary{ID: "explicit@acme", Installed: true, Enabled: true}},
	}
	got := map[string]bool{}
	for _, detail := range mergePluginDetails(stored, discovered, policy) {
		got[detail.Summary.ID] = detail.Summary.Enabled
	}
	if got["tool@acme"] {
		t.Fatalf("a disabled default must narrow an installed plugin: %#v", got)
	}
	if !got["explicit@acme"] {
		t.Fatalf("an explicit enablement must survive the merge: %#v", got)
	}
}

// #52220 end to end: plugin/list and plugin/installed report the effective
// enablement, and the reserved `_default` key is never listed as a plugin
// (Rust app-server/src/request_processors/plugins.rs reports summary.enabled
// after applying the policy).
func TestPluginListReportsActivationDefaultLikeRust(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ConfigTOMLFilename)
	config := "[plugins._default]\nenabled = false\n\n[plugins.\"sample@acme\"]\ninstalled = true\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewPluginService()
	svc.SetMarketplaceConfigPath(configPath)
	for _, summary := range svc.List(&PluginListParams{IncludeInstalled: true}).Plugins {
		if summary.ID == PluginsDefaultConfigKey {
			t.Fatalf("`_default` must not be listed as a plugin: %#v", summary)
		}
		if summary.ID == "sample@acme" && summary.Enabled {
			t.Fatalf("plugin/list ignored the activation default: %#v", summary)
		}
	}
	installed := svc.Installed(nil)
	if len(installed.Plugins) != 1 || installed.Plugins[0].Enabled {
		t.Fatalf("plugin/installed = %#v", installed.Plugins)
	}
}

// #52220: removing an installation must never drop the reserved activation
// default (Rust core-plugins/src/marketplace_policy.rs:238-243 retains it).
func TestRemoveInstalledPluginConfigKeepsActivationDefaultLikeRust(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ConfigTOMLFilename)
	config := "[plugins._default]\nenabled = false\n\n[plugins.\"sample@acme\"]\ninstalled = true\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeInstalledPluginConfig(configPath, PluginsDefaultConfigKey); err != nil {
		t.Fatal(err)
	}
	values, err := readMarketplaceConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	table, ok := values["plugins"].(map[string]any)
	if !ok || table[PluginsDefaultConfigKey] == nil {
		t.Fatalf("activation default was dropped: %#v", values)
	}
}
