package appserver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/plugin"
	"codex_go/session"
)

func newMarketplaceUpgradeRouter(t *testing.T, home string, marketplaceValues map[string]any) (*RuntimeRouter, *plugin.PluginService, *config.ConfigService) {
	t.Helper()
	configService := config.NewConfigService(home)
	if marketplaceValues != nil {
		configService.SetManagedLayers([]config.Layer{{
			Name:   config.LayerSource{Type: config.LayerSourceEnterpriseManaged, File: "managed.toml"},
			Config: marketplaceValues,
		}})
	}
	pluginService := plugin.NewPluginService()
	pluginService.SetCodexHome(home)
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		Config:       configService,
		Skills:       NewSkillsService(nil),
		Plugins:      pluginService,
		ThreadStatus: NewThreadStatusManager(),
		DefaultCWD:   home,
	})
	return router, pluginService, configService
}

func managedGitMarketplaceValues(source string) map[string]any {
	return map[string]any{"marketplaces": map[string]any{
		"system-market": map[string]any{
			"source_type":  "git",
			"source":       source,
			"ref":          "main",
			"sparse_paths": []any{"plugins/a"},
		},
	}}
}

// TestMarketplaceUpgradeUsesManagedLayersLikeRust covers Rust #42149's
// app-server path: source/ref/sparse settings come from the effective merged
// configuration, and activation only writes the installed marketplace, never
// the user config.
func TestMarketplaceUpgradeUsesManagedLayersLikeRust(t *testing.T) {
	home := t.TempDir()
	router, pluginService, _ := newMarketplaceUpgradeRouter(t, home, managedGitMarketplaceValues("https://example.com/system.git"))
	pluginService.SetMarketplaceMaterializer(&marketplaceUpgradeTestMaterializer{write: func(destination string) error {
		return os.WriteFile(filepath.Join(destination, "marker.txt"), []byte("managed"), 0o600)
	}})
	pluginService.SetMarketplaceRevisionResolver(plugin.MarketplaceRevisionResolverFunc(func(source *plugin.ParsedMarketplaceSource) (string, error) {
		if source.URL != "https://example.com/system.git" || source.RefName == nil || *source.RefName != "main" {
			return "", errors.New("unexpected source or ref")
		}
		return "rev-managed", nil
	}))

	response := router.Handle(requestWithParams(t, IntID(1), MethodMarketplaceUpgrade, plugin.MarketplaceUpgradeParams{}))
	if response.Error != nil {
		t.Fatalf("marketplace/upgrade error = %+v", response.Error)
	}
	outcome, ok := response.Result.(*plugin.MarketplaceUpgradeResponse)
	if !ok || outcome == nil {
		t.Fatalf("marketplace/upgrade result = %#v", response.Result)
	}
	if len(outcome.Errors) != 0 {
		t.Fatalf("errors = %#v", outcome.Errors)
	}
	root := filepath.Join(home, plugin.InstalledMarketplacesDir, "system-market")
	if len(outcome.SelectedMarketplaces) != 1 || outcome.SelectedMarketplaces[0] != "system-market" {
		t.Fatalf("selected = %#v", outcome.SelectedMarketplaces)
	}
	if len(outcome.UpgradedRoots) != 1 || outcome.UpgradedRoots[0] != root {
		t.Fatalf("roots = %#v, want %q", outcome.UpgradedRoots, root)
	}
	if data, err := os.ReadFile(filepath.Join(root, "marker.txt")); err != nil || string(data) != "managed" {
		t.Fatalf("installed marker = %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(home, plugin.ConfigTOMLFilename)); !os.IsNotExist(err) {
		t.Fatalf("upgrade wrote the user config, stat error = %v", err)
	}
}

// TestMarketplaceUpgradeReloadFailureRollsBackLikeRust covers the activation
// check on the reload path: when the initiating loader can no longer read the
// configuration, the previous snapshot stays active.
func TestMarketplaceUpgradeReloadFailureRollsBackLikeRust(t *testing.T) {
	home := t.TempDir()
	router, pluginService, _ := newMarketplaceUpgradeRouter(t, home, managedGitMarketplaceValues("https://example.com/system.git"))
	root := filepath.Join(home, plugin.InstalledMarketplacesDir, "system-market")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	pluginService.SetMarketplaceMaterializer(&marketplaceUpgradeTestMaterializer{write: func(destination string) error {
		if err := os.WriteFile(filepath.Join(destination, "marker.txt"), []byte("new"), 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(home, plugin.ConfigTOMLFilename), []byte("this is not toml {{{\n"), 0o600)
	}})
	pluginService.SetMarketplaceRevisionResolver(plugin.MarketplaceRevisionResolverFunc(func(*plugin.ParsedMarketplaceSource) (string, error) {
		return "rev-new", nil
	}))

	response := router.Handle(requestWithParams(t, IntID(1), MethodMarketplaceUpgrade, plugin.MarketplaceUpgradeParams{}))
	if response.Error != nil {
		t.Fatalf("marketplace/upgrade error = %+v", response.Error)
	}
	outcome := response.Result.(*plugin.MarketplaceUpgradeResponse)
	if len(outcome.UpgradedRoots) != 0 {
		t.Fatalf("reload failure activated a snapshot: %#v", outcome.UpgradedRoots)
	}
	if len(outcome.Errors) != 1 || !strings.Contains(outcome.Errors[0].Message, "failed to reload config while checking marketplace upgrade") {
		t.Fatalf("errors = %#v", outcome.Errors)
	}
	if data, err := os.ReadFile(filepath.Join(root, "marker.txt")); err != nil || string(data) != "old" {
		t.Fatalf("previous snapshot = %q, %v", data, err)
	}
}

// TestPluginStartupAutoUpgradesMergedMarketplacesLikeRust covers Rust
// #42149's startup synchronization and its effective-plugins-changed refresh.
func TestPluginStartupAutoUpgradesMergedMarketplacesLikeRust(t *testing.T) {
	home := t.TempDir()
	router, pluginService, _ := newMarketplaceUpgradeRouter(t, home, managedGitMarketplaceValues("https://example.com/system.git"))
	router.services.StartupCWD = home
	done := make(chan struct{}, 1)
	pluginService.SetMarketplaceMaterializer(&marketplaceUpgradeTestMaterializer{write: func(destination string) error {
		defer func() { done <- struct{}{} }()
		return os.WriteFile(filepath.Join(destination, "marker.txt"), []byte("startup"), 0o600)
	}})
	pluginService.SetMarketplaceRevisionResolver(plugin.MarketplaceRevisionResolverFunc(func(*plugin.ParsedMarketplaceSource) (string, error) {
		return "rev-startup", nil
	}))
	before := router.testMCPEpoch()

	router.startPluginStartupTasks()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("startup marketplace upgrade did not run")
	}
	root := filepath.Join(home, plugin.InstalledMarketplacesDir, "system-market")
	deadline := time.After(5 * time.Second)
	for {
		if data, err := os.ReadFile(filepath.Join(root, "marker.txt")); err == nil && string(data) == "startup" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("startup marketplace upgrade did not activate the snapshot")
		case <-time.After(10 * time.Millisecond):
		}
	}
	for router.testMCPEpoch() == before {
		select {
		case <-deadline:
			t.Fatal("startup marketplace upgrade did not refresh derived plugin state")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestPluginStartupFallsBackToDefaultsAfterInvalidConfigLikeRust covers Rust's
// PluginStartupConfig::Defaults selection: an invalid user config is skipped,
// but enterprise-managed marketplaces still synchronize.
func TestPluginStartupFallsBackToDefaultsAfterInvalidConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, plugin.ConfigTOMLFilename), []byte("not toml {{{\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	router, pluginService, _ := newMarketplaceUpgradeRouter(t, home, managedGitMarketplaceValues("https://example.com/system.git"))
	router.services.StartupCWD = home
	pluginService.SetMarketplaceMaterializer(&marketplaceUpgradeTestMaterializer{write: func(destination string) error {
		return os.WriteFile(filepath.Join(destination, "marker.txt"), []byte("defaults"), 0o600)
	}})
	pluginService.SetMarketplaceRevisionResolver(plugin.MarketplaceRevisionResolverFunc(func(*plugin.ParsedMarketplaceSource) (string, error) {
		return "rev-defaults", nil
	}))

	router.startPluginStartupTasks()

	root := filepath.Join(home, plugin.InstalledMarketplacesDir, "system-market")
	deadline := time.After(5 * time.Second)
	for {
		if data, err := os.ReadFile(filepath.Join(root, "marker.txt")); err == nil && string(data) == "defaults" {
			return
		}
		select {
		case <-deadline:
			t.Fatal("defaults loader did not synchronize the managed marketplace")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type marketplaceUpgradeTestMaterializer struct {
	write func(destination string) error
}

func (m *marketplaceUpgradeTestMaterializer) MaterializeMarketplace(_ *plugin.ParsedMarketplaceSource, _ []string, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	return m.write(destination)
}
