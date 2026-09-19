package appserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/plugin"
)

// TestRemoteInstalledPluginDisplayRefreshPreservesDerivedCachesLikeRust mirrors
// the app-server reach of Rust mcp_resource/plugin_metadata_refresh.rs (#46309):
// a display-only installed-metadata refresh republishes the payload but keeps the
// derived skill and MCP caches, while a behavioral change invalidates them.
func TestRemoteInstalledPluginDisplayRefreshPreservesDerivedCachesLikeRust(t *testing.T) {
	home := t.TempDir()
	writeCachedRemotePluginForTest(t, home, remoteInstalledGlobalMarketplace, "demo-plugin")

	var mu sync.Mutex
	displayName := "Demo plugin"
	status := "AVAILABLE"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ps/plugins/installed" {
			http.NotFound(w, request)
			return
		}
		mu.Lock()
		body := fmt.Sprintf(`{"plugins":[{"id":"plugins~Plugin_demo","name":"demo-plugin","scope":"GLOBAL","enabled":true,"status":%q,"release":{"display_name":%q,"description":"Demo plugin"}}],"pagination":{"next_page_token":null}}`, status, displayName)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	if err := os.WriteFile(config.ConfigPath(home), []byte(fmt.Sprintf("chatgpt_base_url = %q\n\n[features]\nplugins = true\nremote_plugin = true\n", server.URL)), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	account := auth.NewAccountManager()
	if _, err := account.Login(&auth.LoginAccountParams{
		Type:             "chatgptAuthTokens",
		AccessToken:      "chatgpt-token",
		ChatGPTAccountID: "account-123",
	}); err != nil {
		t.Fatalf("Login(chatgptAuthTokens) error = %v", err)
	}
	plugins := plugin.NewPluginService()
	plugins.SetCodexHome(home)
	router := NewRuntimeRouter(RuntimeServices{
		Config:      config.NewConfigService(home),
		Account:     account,
		AccountHTTP: server.Client(),
		Skills:      NewSkillsService(nil),
		Plugins:     plugins,
	})

	// The first publication establishes the plugin and is expected to change the
	// derived state (there was nothing cached before).
	if _, err := router.reconcileInstalledRemotePlugins(context.Background()); err != nil {
		t.Fatalf("initial reconcile error = %v", err)
	}
	if installedRemoteMetadataDisplayName(t, plugins, "plugins~Plugin_demo") != "Demo plugin" {
		t.Fatal("initial reconcile did not publish the installed plugin")
	}

	seed := func() {
		router.services.Skills.mu.Lock()
		router.services.Skills.cache = map[string]skillsCacheEntry{"seed": {}}
		router.services.Skills.mu.Unlock()
	}
	cachedSkills := func() int {
		router.services.Skills.mu.Lock()
		defer router.services.Skills.mu.Unlock()
		return len(router.services.Skills.cache)
	}

	seed()
	before := router.testMCPEpoch()
	mu.Lock()
	displayName = "Demo plugin (renamed)"
	mu.Unlock()
	if _, err := router.reconcileInstalledRemotePlugins(context.Background()); err != nil {
		t.Fatalf("display-only reconcile error = %v", err)
	}
	if cachedSkills() == 0 {
		t.Fatal("display-only refresh cleared the plugin/skill caches")
	}
	if router.testMCPEpoch() != before {
		t.Fatal("display-only refresh invalidated the MCP runtimes")
	}
	// The renewed display payload is still published for display consumers.
	if got := installedRemoteMetadataDisplayName(t, plugins, "plugins~Plugin_demo"); got != "Demo plugin (renamed)" {
		t.Fatalf("published display name = %q, want the renewed payload", got)
	}

	seed()
	before = router.testMCPEpoch()
	mu.Lock()
	status = "DISABLED_BY_ADMIN"
	mu.Unlock()
	if _, err := router.reconcileInstalledRemotePlugins(context.Background()); err != nil {
		t.Fatalf("behavioral reconcile error = %v", err)
	}
	if cachedSkills() != 0 {
		t.Fatal("a behavioral change left cached plugin/skill resources")
	}
	if router.testMCPEpoch() == before {
		t.Fatal("a behavioral change did not invalidate the MCP runtimes")
	}
}

func installedRemoteMetadataDisplayName(t *testing.T, plugins *plugin.PluginService, remotePluginID string) string {
	t.Helper()
	for _, detail := range plugins.InstalledDetails() {
		if detail.Summary.RemotePluginID == remotePluginID {
			return detail.Summary.DisplayName
		}
	}
	t.Fatalf("installed remote plugin %q was not found", remotePluginID)
	return ""
}
