package appserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"codex_go/config"
	"codex_go/features"
	"codex_go/model"
)

// threadModelCatalogCacheLimit bounds the per-provider catalogs a router keeps.
// Real deployments use one or two providers; the limit only protects embedded
// hosts that churn through request-scoped provider overrides.
const threadModelCatalogCacheLimit = 32

// threadModelCatalogKey identifies the provider/catalog pair a thread resolves
// through (Rust ThreadModelCatalog::for_config compares the provider, catalog,
// and API-key discovery setting before reusing the startup manager).
func threadModelCatalogKey(cfg *config.Config) (string, bool) {
	if cfg == nil || len(cfg.Values) == 0 {
		return "", false
	}
	providerID := configuredModelProviderID(cfg)
	if providerID == "" {
		return "", false
	}
	openAIBaseURL := strings.TrimSpace(stringConfigValue(cfg, "openai_base_url"))
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), providerID, openAIBaseURL)
	if err != nil || providerInfo == nil {
		return "", false
	}
	payload := map[string]any{
		"provider":                providerID,
		"provider_config":         threadModelCatalogProviderConfig(configValues(cfg), providerID),
		"base_url":                openAIBaseURL,
		"model_catalog_json":      cfg.Values["model_catalog_json"],
		"api_key_model_discovery": features.Enabled(cfg.FeatureSettings(), "api_key_model_discovery"),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(raw)
	return providerID + ":" + hex.EncodeToString(sum[:]), true
}

func threadModelCatalogProviderConfig(values map[string]any, providerID string) any {
	providers, ok := values["model_providers"].(map[string]any)
	if !ok {
		return nil
	}
	return providers[providerID]
}

// rememberStartupModelsCatalogKey records the catalog identity backing
// services.Models. Threads resolving to the same provider/catalog reuse that
// manager instead of building a duplicate remote catalog.
func (r *RuntimeRouter) rememberStartupModelsCatalogKey(cfg *config.Config) {
	if r == nil {
		return
	}
	key, ok := threadModelCatalogKey(cfg)
	if !ok {
		return
	}
	r.threadModelsMu.Lock()
	if r.startupModelsKey != key {
		r.threadModels = nil
	}
	r.startupModelsKey = key
	r.threadModelsMu.Unlock()
}

func (r *RuntimeRouter) clearThreadModelCatalogs() {
	if r == nil {
		return
	}
	r.threadModelsMu.Lock()
	r.threadModels = nil
	r.threadModelsMu.Unlock()
}

// modelsManagerForConfig resolves the catalog manager that belongs to cfg's
// provider and configured catalog. It returns nil when the configuration cannot
// be resolved, in which case callers keep the startup catalog.
func (r *RuntimeRouter) modelsManagerForConfig(cfg *config.Config) model.ModelsManager {
	if r == nil {
		return nil
	}
	key, ok := threadModelCatalogKey(cfg)
	if !ok {
		return nil
	}
	r.threadModelsMu.Lock()
	startupKey := r.startupModelsKey
	cached := r.threadModels[key]
	r.threadModelsMu.Unlock()
	if startupKey == key {
		return r.requireModels().Manager()
	}
	if cached != nil {
		return cached
	}
	var requirements *config.ConfigRequirementsReadResponse
	if r.services.Config != nil {
		requirements = r.services.Config.Requirements()
	}
	manager, err := buildAccountScopedModelsManagerForCWD(
		r.codexHomeForRollout(),
		&config.ConfigReadResponse{Config: configValues(cfg)},
		requirements,
		r.startupCWD(),
	)
	if err != nil || manager == nil {
		return nil
	}
	// Rust initializes a thread catalog before sampling or memory extraction so
	// provider-advertised capabilities and instructions are available. Go's
	// resolver is synchronous, so load the cache or fetch once when the new
	// manager is first created (subsequent lookups share the cached manager).
	manager.RawModelCatalog(model.RefreshOnlineIfUncached)
	r.threadModelsMu.Lock()
	if r.threadModels == nil || len(r.threadModels) >= threadModelCatalogCacheLimit {
		r.threadModels = map[string]model.ModelsManager{}
	}
	if existing := r.threadModels[key]; existing != nil {
		manager = existing
	} else {
		r.threadModels[key] = manager
	}
	r.threadModelsMu.Unlock()
	return manager
}
