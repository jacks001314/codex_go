package app

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/plugin"
)

// Rust parity: codex-rs/app-server/src/request_processors/plugins.rs
// `installed_remote_plugin_summary_to_info` (#61865).

func writeInstalledRemotePlugin(t *testing.T, store *plugin.PluginStore, pluginID *plugin.PluginId, version string, remotePluginID string) {
	t.Helper()
	root := store.PluginRoot(pluginID, version)
	manifestDir := filepath.Join(root, ".codex-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("create manifest dir: %v", err)
	}
	manifest := []byte(`{"name":"` + pluginID.PluginName + `","version":"` + version + `"}`)
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := store.WriteRemotePluginID(pluginID, remotePluginID); err != nil {
		t.Fatalf("WriteRemotePluginID() error = %v", err)
	}
}

func TestAppendRemotePluginMarketplaceReportsInstalledLocalVersionLikeRust(t *testing.T) {
	store, err := plugin.NewPluginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPluginStore() error = %v", err)
	}
	pluginID, err := plugin.NewPluginId("linear", "market")
	if err != nil {
		t.Fatalf("NewPluginId() error = %v", err)
	}
	writeInstalledRemotePlugin(t, store, pluginID, "1.2.3", "plugins~Plugin_a")

	catalogVersion := "2.0.0"
	remote := []plugin.RemoteCatalogPlugin{{
		ID:      "plugins~Plugin_a",
		Name:    "linear",
		Release: plugin.RemoteCatalogRelease{Version: &catalogVersion},
	}}
	entries := appendRemotePluginMarketplace(nil, "market", remote, map[string]bool{"plugins~Plugin_a": true}, store)
	if len(entries) != 1 || len(entries[0].Plugins) != 1 {
		t.Fatalf("entries = %+v, want one plugin", entries)
	}
	summary := entries[0].Plugins[0]
	if summary.Version == nil || *summary.Version != "2.0.0" {
		t.Fatalf("version = %v, want the catalog version", summary.Version)
	}
	if summary.LocalVersion == nil || *summary.LocalVersion != "1.2.3" {
		t.Fatalf("local version = %v, want the installed package version", summary.LocalVersion)
	}
	if !summary.Installed || !summary.Enabled {
		t.Fatalf("summary = %+v, want the installed state", summary)
	}
}

func TestAppendRemotePluginMarketplaceLeavesUnprovenLocalVersionUnsetLikeRust(t *testing.T) {
	store, err := plugin.NewPluginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPluginStore() error = %v", err)
	}
	pluginID, err := plugin.NewPluginId("linear", "market")
	if err != nil {
		t.Fatalf("NewPluginId() error = %v", err)
	}
	// The cached package belongs to a different remote plugin, so its version
	// must not be reported for this catalog identity.
	writeInstalledRemotePlugin(t, store, pluginID, "1.2.3", "plugins~Plugin_other")

	catalogVersion := "2.0.0"
	remote := []plugin.RemoteCatalogPlugin{
		{ID: "plugins~Plugin_a", Name: "linear", Release: plugin.RemoteCatalogRelease{Version: &catalogVersion}},
		{ID: "plugins~Plugin_b", Name: "calendar", Release: plugin.RemoteCatalogRelease{Version: &catalogVersion}},
	}
	entries := appendRemotePluginMarketplace(nil, "market", remote, map[string]bool{"plugins~Plugin_a": true}, store)
	if len(entries) != 1 || len(entries[0].Plugins) != 2 {
		t.Fatalf("entries = %+v, want two plugins", entries)
	}
	if local := entries[0].Plugins[0].LocalVersion; local != nil {
		t.Fatalf("local version = %q for a mismatched receipt", *local)
	}
	if local := entries[0].Plugins[1].LocalVersion; local != nil {
		t.Fatalf("local version = %q for an uninstalled plugin", *local)
	}
}
