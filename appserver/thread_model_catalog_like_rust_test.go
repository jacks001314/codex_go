package appserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

func threadCatalogServer(t *testing.T, supportsUpdates bool) (*httptest.Server, *int32) {
	t.Helper()
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"models":[{"slug":"thread-catalog-model","display_name":"Thread Catalog","visibility":"list","supported_in_api":true,"priority":1,"supports_reasoning_effort_updates":%t}]}`, supportsUpdates)
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

// Rust #61421: a thread resolves model metadata through its own provider's
// catalog, so switching the default provider cannot leak the startup catalog's
// capabilities into an existing thread.
func TestThreadModelCatalogUsesOwningProviderLikeRust(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("THREAD_CATALOG_KEY", "test-key")
	alpha, alphaRequests := threadCatalogServer(t, true)
	beta, _ := threadCatalogServer(t, false)
	home := t.TempDir()
	configBody := fmt.Sprintf(`model_provider = "beta"
features.api_key_model_discovery = true

[model_providers.alpha]
name = "OpenAI"
base_url = "%s/v1"
model_catalog_url = "%s/v1/models"
env_key = "THREAD_CATALOG_KEY"
requires_openai_auth = true

[model_providers.beta]
name = "OpenAI"
base_url = "%s/v1"
model_catalog_url = "%s/v1/models"
env_key = "THREAD_CATALOG_KEY"
requires_openai_auth = true
`, alpha.URL, alpha.URL, beta.URL, beta.URL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, nil)
	t.Cleanup(func() { _ = router.Close() })
	router.requireModels().Manager().RawModelCatalog(model.RefreshOnlineIfUncached)

	alphaConfig, err := router.effectiveConfigForTurn(&turn.TurnStartParams{Config: map[string]any{"model_provider": "alpha"}})
	if err != nil || alphaConfig == nil {
		t.Fatalf("alpha config error = %v", err)
	}
	betaConfig, err := router.effectiveConfigForTurn(&turn.TurnStartParams{})
	if err != nil || betaConfig == nil {
		t.Fatalf("beta config error = %v", err)
	}

	alphaInfo := router.modelInfoForRuntimeWithConfig("thread-catalog-model", alphaConfig)
	if alphaInfo == nil || !alphaInfo.SupportsReasoningEffortUpdates {
		t.Fatalf("alpha model info = %#v, want supports_reasoning_effort_updates", alphaInfo)
	}
	alphaWarmRequests := atomic.LoadInt32(alphaRequests)
	betaInfo := router.modelInfoForRuntimeWithConfig("thread-catalog-model", betaConfig)
	if betaInfo == nil || betaInfo.SupportsReasoningEffortUpdates {
		t.Fatalf("beta model info = %#v, want startup catalog capability", betaInfo)
	}
	if got := atomic.LoadInt32(alphaRequests); got != alphaWarmRequests {
		t.Fatalf("alpha catalog requests = %d, want %d (thread manager reused)", got, alphaWarmRequests)
	}

	// A second lookup in the same provider keeps the cached thread manager.
	_ = router.modelInfoForRuntimeWithConfig("thread-catalog-model", alphaConfig)
	if got := atomic.LoadInt32(alphaRequests); got != alphaWarmRequests {
		t.Fatalf("alpha catalog requests after reuse = %d, want %d", got, alphaWarmRequests)
	}
}
