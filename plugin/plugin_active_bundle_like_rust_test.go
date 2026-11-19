package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReplaceInstalledRemotePluginsDetectsExternalBundleActivationLikeRust
// mirrors Rust #62105: the active bundle identity (directory version plus the
// persisted remote plugin id) is part of the runtime state, so a bundle another
// process activated refreshes the runtime even when the catalog payload is
// unchanged.
func TestReplaceInstalledRemotePluginsDetectsExternalBundleActivationLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	store, err := NewPluginStore(home)
	if err != nil {
		t.Fatalf("NewPluginStore: %v", err)
	}
	pluginIDValue, err := ParsePluginId(pluginID("test", remoteMetadataMarketplace))
	if err != nil {
		t.Fatalf("ParsePluginId: %v", err)
	}
	if err := os.MkdirAll(store.PluginRoot(pluginIDValue, "1.0.0"), 0o755); err != nil {
		t.Fatalf("materialize version 1.0.0: %v", err)
	}

	detail := remoteMetadataPlugin("first")
	if changed := service.ReplaceInstalledRemotePlugins(remoteMetadataMarketplace, []PluginDetail{detail}); !changed {
		t.Fatal("the first publication must report a change")
	}
	stored := installedDetailByRemoteID(t, service, "plugin-test")
	if stored.ActiveBundle == nil || stored.ActiveBundle.DirectoryVersion != "1.0.0" {
		t.Fatalf("active bundle = %#v, want the materialized 1.0.0 bundle", stored.ActiveBundle)
	}
	if changed := service.ReplaceInstalledRemotePlugins(remoteMetadataMarketplace, []PluginDetail{detail}); changed {
		t.Fatal("an unchanged catalog payload must keep the runtime")
	}

	// Another process activates a newer bundle and refreshes the install receipt.
	if err := os.MkdirAll(store.PluginRoot(pluginIDValue, "2.0.0"), 0o755); err != nil {
		t.Fatalf("materialize version 2.0.0: %v", err)
	}
	if err := store.WriteRemotePluginID(pluginIDValue, "plugin-test-remote"); err != nil {
		t.Fatalf("WriteRemotePluginID: %v", err)
	}
	if changed := service.ReplaceInstalledRemotePlugins(remoteMetadataMarketplace, []PluginDetail{detail}); !changed {
		t.Fatal("an externally activated bundle must refresh the runtime")
	}
	stored = installedDetailByRemoteID(t, service, "plugin-test")
	if stored.ActiveBundle == nil || stored.ActiveBundle.DirectoryVersion != "2.0.0" {
		t.Fatalf("active bundle = %#v, want the activated 2.0.0 bundle", stored.ActiveBundle)
	}
	if stored.ActiveBundle.RemotePluginID == nil || *stored.ActiveBundle.RemotePluginID != "plugin-test-remote" {
		t.Fatalf("active bundle remote id = %#v, want the persisted receipt", stored.ActiveBundle.RemotePluginID)
	}

	// A disabled plugin keeps no bundle, so enablement alone decides the runtime.
	disabled := remoteMetadataPlugin("first")
	disabled.Summary.Enabled = false
	if changed := service.ReplaceInstalledRemotePlugins(remoteMetadataMarketplace, []PluginDetail{disabled}); !changed {
		t.Fatal("disabling must refresh the runtime")
	}
	if bundle := installedDetailByRemoteID(t, service, "plugin-test").ActiveBundle; bundle != nil {
		t.Fatalf("disabled plugin bundle = %#v, want none", bundle)
	}
	if _, statErr := os.Stat(filepath.Join(home, PluginsCacheDir)); statErr != nil {
		t.Fatalf("plugin cache root missing: %v", statErr)
	}
}
