package plugin

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Mirrors the two-plugin version gate in Rust #61129's first full list test.
func TestCacheRefreshBatchUpdatesOnlyNewVersions(t *testing.T) {
	service := NewPluginService()
	service.SetCodexHome(t.TempDir())
	publish := false
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(source *ParsedMarketplaceSource, destination string) error {
		version, marker := "1.0.0", "production"
		if publish {
			marker = "staging"
			if source.URL == "https://example.test/release.git" {
				version = "1.1.0"
			}
		}
		if err := os.MkdirAll(filepath.Join(destination, ".codex-plugin"), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, ".codex-plugin", "plugin.json"), []byte("{\"name\":\"plugin\",\"version\":\""+version+"\"}"), 0600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, "marker"), []byte(marker), 0600)
	}))
	for _, name := range []string{"docs", "release"} {
		detail := PluginDetail{Summary: PluginSummary{ID: name + "@team", Name: name, MarketplaceName: "team", Installed: true, Enabled: true, Source: PluginSource{Type: "git", URL: "https://example.test/" + name + ".git"}}}
		if _, err := service.refreshMarketplacePluginVersion(&detail); err != nil {
			t.Fatal(err)
		}
		service.AddPlugin(detail)
	}
	publish = true
	completed := make(chan struct{})
	if !service.StartNonCuratedCacheRefresh(func() { close(completed) }) {
		t.Fatal("batch not started")
	}
	select {
	case <-completed:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not finish")
	}
	for name, want := range map[string]string{"docs": "production", "release": "staging"} {
		root := marketplacePluginInstallDestination(service.marketplaceInstallRoot, "team", name)
		actual, err := os.ReadFile(filepath.Join(root, "marker"))
		if err != nil || string(actual) != want {
			t.Fatalf("%s marker=%q err=%v want=%q", name, actual, err, want)
		}
	}
	if service.StartNonCuratedCacheRefresh(nil) {
		t.Fatal("completed sources unnecessarily refreshed")
	}
}
