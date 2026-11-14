package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheRefreshPreservesSameVersionAndFailedDownload(t *testing.T) {
	service := NewPluginService()
	service.SetCodexHome(t.TempDir())
	detail := &PluginDetail{Summary: PluginSummary{ID: "docs@team", Name: "docs", MarketplaceName: "team", Source: PluginSource{Type: "git", URL: "https://example.test/docs.git"}}}
	version, contents := "1.0.0", "production"
	fail := false
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(_ *ParsedMarketplaceSource, destination string) error {
		if fail {
			return errors.New("download failed")
		}
		if err := os.MkdirAll(filepath.Join(destination, ".codex-plugin"), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, ".codex-plugin", "plugin.json"), []byte(`{"name":"docs","version":"`+version+`"}`), 0600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, "marker"), []byte(contents), 0600)
	}))
	if changed, err := service.refreshMarketplacePluginVersion(detail); err != nil || !changed {
		t.Fatalf("initial=%v %v", changed, err)
	}
	root := marketplacePluginInstallDestination(service.marketplaceInstallRoot, "team", "docs")
	assertMarker := func(want string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "marker"))
		if err != nil || string(data) != want {
			t.Fatalf("marker=%q %v want %q", data, err, want)
		}
	}
	contents = "staging"
	if changed, err := service.refreshMarketplacePluginVersion(detail); err != nil || changed {
		t.Fatalf("same version=%v %v", changed, err)
	}
	assertMarker("production")
	fail = true
	if _, err := service.refreshMarketplacePluginVersion(detail); err == nil {
		t.Fatal("missing download error")
	}
	assertMarker("production")
	fail = false
	version = "1.1.0"
	if changed, err := service.refreshMarketplacePluginVersion(detail); err != nil || !changed {
		t.Fatalf("new version=%v %v", changed, err)
	}
	assertMarker("staging")
	contents = "forced same version"
	if changed, err := service.refreshMarketplacePluginVersionChecked(detail, false, true); err != nil || !changed {
		t.Fatalf("force=%v %v", changed, err)
	}
	assertMarker("forced same version")
}
