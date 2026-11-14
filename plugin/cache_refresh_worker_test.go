package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCacheRefreshWorkerCoalescesAndUpdatesMetadata(t *testing.T) {
	service := NewPluginService()
	service.SetCodexHome(t.TempDir())
	service.AddPlugin(PluginDetail{Summary: PluginSummary{ID: "docs@team", Name: "docs", MarketplaceName: "team", Installed: true, Enabled: true, Source: PluginSource{Type: "git", URL: "https://example.test/docs.git"}}})
	started, release, changed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(_ *ParsedMarketplaceSource, destination string) error {
		close(started)
		<-release
		if err := os.MkdirAll(filepath.Join(destination, ".codex-plugin"), 0755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, ".codex-plugin", "plugin.json"), []byte(`{"name":"docs","version":"2.0.0"}`), 0600)
	}))
	if !service.StartNonCuratedCacheRefresh(func() { close(changed) }) {
		t.Fatal("worker not started")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("download not started")
	}
	if service.StartNonCuratedCacheRefresh(nil) {
		t.Fatal("duplicate worker started")
	}
	unblock()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("no change notification")
	}
	service.mu.Lock()
	actual := service.plugins["docs@team"]
	service.mu.Unlock()
	if stringPtrValue(actual.Summary.Version) != "2.0.0" {
		t.Fatalf("version=%v", actual.Summary.Version)
	}
	if service.StartNonCuratedCacheRefresh(nil) {
		t.Fatal("successful source was checked twice")
	}
}

func TestCacheRefreshWorkerQueuesForceDuringOrdinaryRefresh(t *testing.T) {
	service := NewPluginService()
	service.SetCodexHome(t.TempDir())
	detail := PluginDetail{Summary: PluginSummary{ID: "docs@team", Name: "docs", MarketplaceName: "team", Installed: true, Enabled: true, Source: PluginSource{Type: "git", URL: "https://example.test/docs.git"}}}
	service.AddPlugin(detail)
	started, release, changed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	calls := 0
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(_ *ParsedMarketplaceSource, destination string) error {
		calls++
		if calls == 1 {
			close(started)
			<-release
		}
		if err := os.MkdirAll(filepath.Join(destination, ".codex-plugin"), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(destination, ".codex-plugin", "plugin.json"), []byte(`{"name":"docs","version":"1.0.0"}`), 0600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, "marker"), []byte(fmt.Sprint(calls)), 0600)
	}))
	service.StartNonCuratedCacheRefresh(func() { close(changed) })
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download not started")
	}
	service.StartNonCuratedCacheRefreshWithMode(true, nil)
	service.StartNonCuratedCacheRefreshWithMode(true, nil)
	unblock()
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("no completion notification")
	}
	if calls != 2 {
		t.Fatalf("downloads=%d, want ordinary plus one queued force", calls)
	}
	destination := marketplacePluginInstallDestination(service.marketplaceInstallRoot, "team", "docs")
	marker, err := os.ReadFile(filepath.Join(destination, "marker"))
	if err != nil || string(marker) != "2" {
		t.Fatalf("forced same-version content=%q, error=%v", marker, err)
	}
}

func cacheRefreshConfig(url string) map[string]any {
	return map[string]any{"plugins": map[string]any{"docs@team": map[string]any{"source_type": "git", "source": url, "installed": true}}}
}

func TestConfiguredCacheRefreshDiscoversUnloadedInstallation(t *testing.T) {
	service := NewPluginService()
	service.SetCodexHome(t.TempDir())
	values := cacheRefreshConfig("https://example.test/docs.git")
	service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(_ *ParsedMarketplaceSource, destination string) error {
		if err := os.MkdirAll(filepath.Join(destination, ".codex-plugin"), 0755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, ".codex-plugin", "plugin.json"), []byte(`{"name":"docs","version":"3.0.0"}`), 0600)
	}))
	changed := make(chan struct{})
	if !service.StartConfiguredPluginCacheRefresh(values, false, func() (map[string]any, error) { return values, nil }, func() { close(changed) }) {
		t.Fatal("configured installation not scheduled")
	}
	select {
	case <-changed:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh did not finish")
	}
	service.mu.Lock()
	actual, ok := service.plugins["docs@team"]
	service.mu.Unlock()
	if !ok || stringPtrValue(actual.Summary.Version) != "3.0.0" {
		t.Fatalf("missing refreshed installation: %#v", actual.Summary)
	}
}

func TestCacheRefreshRejectsChangedConfigurationBeforeActivation(t *testing.T) {
	for _, mode := range []string{"removed", "source_changed", "read_failure"} {
		t.Run(mode, func(t *testing.T) {
			service := NewPluginService()
			service.SetCodexHome(t.TempDir())
			values := cacheRefreshConfig("https://example.test/docs.git")
			detail := configuredPluginsFromConfig(values, disabledManifestCache())[0]
			destination := marketplacePluginInstallDestination(service.marketplaceInstallRoot, "team", "docs")
			if err := os.MkdirAll(filepath.Join(destination, ".codex-plugin"), 0755); err != nil {
				t.Fatal(err)
			}
			old := []byte(`{"name":"docs","version":"1.0.0"}`)
			manifest := filepath.Join(destination, ".codex-plugin", "plugin.json")
			if err := os.WriteFile(manifest, old, 0600); err != nil {
				t.Fatal(err)
			}
			service.SetMarketplacePluginMaterializer(MarketplacePluginMaterializerFunc(func(_ *ParsedMarketplaceSource, candidate string) error {
				if err := os.MkdirAll(filepath.Join(candidate, ".codex-plugin"), 0755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(candidate, ".codex-plugin", "plugin.json"), []byte(`{"name":"docs","version":"2.0.0"}`), 0600)
			}))
			reload := func() (map[string]any, error) {
				switch mode {
				case "removed":
					return map[string]any{}, nil
				case "source_changed":
					return cacheRefreshConfig("https://example.test/other.git"), nil
				default:
					return nil, fmt.Errorf("config unavailable")
				}
			}
			changed, err := service.refreshMarketplacePluginVersionChecked(&detail, false, false, reload)
			if err == nil || changed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
			actual, err := os.ReadFile(manifest)
			if err != nil || string(actual) != string(old) {
				t.Fatalf("active cache changed: %q, %v", actual, err)
			}
		})
	}
}
