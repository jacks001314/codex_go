package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/features"
	"codex_go/plugin"
	"codex_go/session"
)

func newPluginRefreshTestRouter(t *testing.T, home string) *RuntimeRouter {
	t.Helper()
	return NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		Config:       config.NewConfigService(home),
		Skills:       NewSkillsService(nil),
		Plugins:      plugin.NewPluginService(),
		ThreadStatus: NewThreadStatusManager(),
	})
}

func (r *RuntimeRouter) testMCPEpoch() uint64 {
	r.mcpRuntimes.mu.Lock()
	defer r.mcpRuntimes.mu.Unlock()
	return r.mcpRuntimes.globalEpoch
}

// TestEffectivePluginsChangedRefreshesDerivedStateLikeRust mirrors Rust
// on_effective_plugins_changed: the plugin/skill caches are cleared and the
// loaded threads' config-derived MCP runtimes are invalidated so the next use
// rebuilds them.
func TestEffectivePluginsChangedRefreshesDerivedStateLikeRust(t *testing.T) {
	home := t.TempDir()
	router := newPluginRefreshTestRouter(t, home)
	router.services.Skills.mu.Lock()
	router.services.Skills.cache = map[string]skillsCacheEntry{"seed": {}}
	router.services.Skills.mu.Unlock()
	before := router.testMCPEpoch()

	router.effectivePluginsChanged()

	router.services.Skills.mu.Lock()
	cached := len(router.services.Skills.cache)
	router.services.Skills.mu.Unlock()
	if cached != 0 {
		t.Fatalf("plugin change left %d skills cache entries", cached)
	}
	if router.testMCPEpoch() == before {
		t.Fatal("plugin change did not invalidate the MCP runtimes")
	}
}

// TestMarketplaceUpgradeWithoutChangesDoesNotRefreshLikeRust covers Rust's
// `!outcome.upgraded_roots.is_empty()` guard: an upgrade that changed nothing
// leaves the derived runtime state alone.
func TestMarketplaceUpgradeWithoutChangesDoesNotRefreshLikeRust(t *testing.T) {
	home := t.TempDir()
	router := newPluginRefreshTestRouter(t, home)
	before := router.testMCPEpoch()

	response := router.Handle(requestWithParams(t, IntID(1), MethodMarketplaceUpgrade, plugin.MarketplaceUpgradeParams{}))
	if response.Error != nil {
		t.Fatalf("marketplace/upgrade error = %+v", response.Error)
	}
	upgraded, ok := response.Result.(*plugin.MarketplaceUpgradeResponse)
	if !ok || upgraded == nil {
		t.Fatalf("marketplace/upgrade result = %#v", response.Result)
	}
	if len(upgraded.UpgradedRoots) != 0 {
		t.Fatalf("upgraded roots = %#v, want none", upgraded.UpgradedRoots)
	}
	if router.testMCPEpoch() != before {
		t.Fatal("upgrade without changed roots refreshed the runtimes")
	}
}

// TestConfigValueWriteClearsDerivedCachesLikeRust covers Rust
// config_value_write's handle_config_mutation call.
func TestConfigValueWriteClearsDerivedCachesLikeRust(t *testing.T) {
	home := t.TempDir()
	router := newPluginRefreshTestRouter(t, home)
	router.services.Skills.mu.Lock()
	router.services.Skills.cache = map[string]skillsCacheEntry{"seed": {}}
	router.services.Skills.mu.Unlock()

	response := router.Handle(requestWithParams(t, IntID(1), MethodConfigValueWrite, config.ConfigValueWriteParams{
		KeyPath:       "tui.theme",
		Value:         "dark",
		MergeStrategy: config.MergeReplace,
	}))
	if response.Error != nil {
		t.Fatalf("config/value/write error = %+v", response.Error)
	}
	router.services.Skills.mu.Lock()
	cached := len(router.services.Skills.cache)
	router.services.Skills.mu.Unlock()
	if cached != 0 {
		t.Fatalf("config/value/write left %d skills cache entries", cached)
	}
}

// TestExperimentalFeatureSetRefreshesLikeRust covers Rust
// experimental_feature_enablement_set: caches are cleared and a non-empty
// enablement change reloads the user config for the loaded threads.
func TestExperimentalFeatureSetRefreshesLikeRust(t *testing.T) {
	home := t.TempDir()
	router := newPluginRefreshTestRouter(t, home)
	seedCache := func() {
		t.Helper()
		router.services.Skills.mu.Lock()
		router.services.Skills.cache = map[string]skillsCacheEntry{"seed": {}}
		router.services.Skills.mu.Unlock()
	}
	cacheEntries := func() int {
		router.services.Skills.mu.Lock()
		defer router.services.Skills.mu.Unlock()
		return len(router.services.Skills.cache)
	}

	seedCache()
	before := router.testMCPEpoch()
	response := router.Handle(requestWithParams(t, IntID(1), MethodExperimentalFeatureSet, features.FeatureEnablementSetParams{
		Enablement: map[string]bool{"mentions_v2": true},
	}))
	if response.Error != nil {
		t.Fatalf("experimentalFeature/enablement/set error = %+v", response.Error)
	}
	if cacheEntries() != 0 {
		t.Fatal("enablement change did not clear the skills cache")
	}
	if router.testMCPEpoch() == before {
		t.Fatal("enablement change did not reload the loaded threads")
	}

	// An unknown key changes nothing, so no reload happens.
	seedCache()
	before = router.testMCPEpoch()
	response = router.Handle(requestWithParams(t, IntID(2), MethodExperimentalFeatureSet, features.FeatureEnablementSetParams{
		Enablement: map[string]bool{"not_a_feature": true},
	}))
	if response.Error != nil {
		t.Fatalf("experimentalFeature/enablement/set error = %+v", response.Error)
	}
	if router.testMCPEpoch() != before {
		t.Fatal("unknown enablement key triggered a reload")
	}
}
