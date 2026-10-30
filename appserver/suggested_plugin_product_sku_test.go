package appserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/plugin"
)

// Rust #47116: a turn-scoped suggested-plugin provider is rebuilt when the
// configured product SKU changes, even if the endpoint stays the same.
func TestConfigureSuggestedPluginProviderUsesConfiguredProductSKULikeRust(t *testing.T) {
	var mu sync.Mutex
	var skuHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/ps/plugins/suggested/codex" {
			http.NotFound(w, request)
			return
		}
		mu.Lock()
		skuHeaders = append(skuHeaders, request.Header.Get("OAI-Product-Sku"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"enabled":true,"plugins":[]}`))
	}))
	defer server.Close()

	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.FromChatGPTAuthTokens("token", "account", nil)); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	plugins := plugin.NewPluginService()
	router := NewRuntimeRouter(RuntimeServices{
		Config:  config.NewConfigService(home),
		Plugins: plugins,
	})
	cfg := &config.Config{Values: map[string]any{
		"chatgpt_base_url":     server.URL,
		"apps_mcp_product_sku": "tpp",
		"features": map[string]any{
			"apps": true, "plugins": true, "remote_plugin": true, "tool_suggest": true,
		},
	}}

	router.configureSuggestedPluginProviderForTurn(cfg)
	plugins.PrewarmSuggestedPlugins(context.Background())
	cfg.Values["apps_mcp_product_sku"] = "team"
	router.configureSuggestedPluginProviderForTurn(cfg)
	plugins.PrewarmSuggestedPlugins(context.Background())
	cfg.Values["apps_mcp_product_sku"] = ""
	router.configureSuggestedPluginProviderForTurn(cfg)
	plugins.PrewarmSuggestedPlugins(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(skuHeaders) != 3 || skuHeaders[0] != "tpp" || skuHeaders[1] != "team" || skuHeaders[2] != plugin.CODEXProductSKU {
		t.Fatalf("OAI-Product-Sku headers = %#v", skuHeaders)
	}
}
