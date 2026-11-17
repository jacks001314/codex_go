package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

// Rust parity: codex-rs/core-plugins/src/manager.rs
// `PluginsManager::local_remote_plugin_version` (#61865).

func writeCachedRemotePluginVersion(t *testing.T, store *PluginStore, pluginID *PluginId, version string, withManifest bool) {
	t.Helper()
	root := store.PluginRoot(pluginID, version)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create plugin root: %v", err)
	}
	if !withManifest {
		return
	}
	manifestDir := filepath.Join(root, ".codex-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("create manifest dir: %v", err)
	}
	manifest := []byte(`{"name":"linear","version":"` + version + `"}`)
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func TestLocalRemotePluginVersionRequiresMatchingReceiptLikeRust(t *testing.T) {
	store, err := NewPluginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPluginStore() error = %v", err)
	}
	pluginID, err := NewPluginId("linear", "openai-curated")
	if err != nil {
		t.Fatalf("NewPluginId() error = %v", err)
	}
	writeCachedRemotePluginVersion(t, store, pluginID, "1.2.3", true)
	if err := store.WriteRemotePluginID(pluginID, "plugins~Plugin_a"); err != nil {
		t.Fatalf("WriteRemotePluginID() error = %v", err)
	}
	if version, ok := store.LocalRemotePluginVersion(pluginID.Key(), "plugins~Plugin_a"); !ok || version != "1.2.3" {
		t.Fatalf("local version = (%q, %v), want the installed directory name", version, ok)
	}
	// The catalog identity decides ownership: same name and version are not
	// enough to claim another package.
	if version, ok := store.LocalRemotePluginVersion(pluginID.Key(), "plugins~Plugin_b"); ok {
		t.Fatalf("local version = %q for a mismatched remote id", version)
	}
}

func TestLocalRemotePluginVersionRequiresManifestAndReceiptLikeRust(t *testing.T) {
	store, err := NewPluginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPluginStore() error = %v", err)
	}
	pluginID, err := NewPluginId("linear", "openai-curated")
	if err != nil {
		t.Fatalf("NewPluginId() error = %v", err)
	}

	// Older caches predate install receipts: the catalog version stands in.
	writeCachedRemotePluginVersion(t, store, pluginID, "1.2.3", true)
	if version, ok := store.LocalRemotePluginVersion(pluginID.Key(), "plugins~Plugin_a"); ok {
		t.Fatalf("local version = %q without an install receipt", version)
	}

	// A receipt without a manifest is not a usable package.
	manifestPath := filepath.Join(store.PluginRoot(pluginID, "1.2.3"), ".codex-plugin", "plugin.json")
	if err := os.Remove(manifestPath); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}
	if err := store.WriteRemotePluginID(pluginID, "plugins~Plugin_a"); err != nil {
		t.Fatalf("WriteRemotePluginID() error = %v", err)
	}
	if version, ok := store.LocalRemotePluginVersion(pluginID.Key(), "plugins~Plugin_a"); ok {
		t.Fatalf("local version = %q without a manifest", version)
	}
}

func TestLocalRemotePluginVersionRejectsInvalidKeysLikeRust(t *testing.T) {
	store, err := NewPluginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewPluginStore() error = %v", err)
	}
	if version, ok := store.LocalRemotePluginVersion("linear", "plugins~Plugin_a"); ok {
		t.Fatalf("local version = %q for a key without a marketplace", version)
	}
}
