package model

import "testing"

// Rust #58361: model lookups return the metadata together with its catalog
// origin, so a synthetic fallback stays distinguishable from bundled, provider,
// or explicitly configured metadata after config overrides.
func TestGetCatalogModelPreservesOriginLikeRust(t *testing.T) {
	reviewer := ModelInfo{Slug: "codex-auto-review", DisplayName: "Auto review", Visibility: VisibilityVisible, SupportedInAPI: true}
	bundled := ModelInfo{Slug: "bundled-model", DisplayName: "Bundled", Visibility: VisibilityVisible, SupportedInAPI: true}
	config := &ModelsManagerConfig{Personality: "none"}

	for _, origin := range []ModelOrigin{ModelOriginBundled, ModelOriginProvider, ModelOriginExplicitConfig} {
		manager := NewStaticModelsManagerWithOrigin(ModelsResponse{Models: []ModelInfo{bundled}}, origin)
		resolved := manager.GetCatalogModel("bundled-model", config)
		if resolved.Origin != origin || resolved.Model.Slug != "bundled-model" {
			t.Fatalf("static lookup = %#v, want origin %q", resolved, origin)
		}
		if synthetic := manager.GetCatalogModel("missing-model", config); synthetic.Origin != ModelOriginSynthetic {
			t.Fatalf("missing static lookup origin = %q, want synthetic", synthetic.Origin)
		}
	}

	endpoint := &recordingModelsEndpoint{responses: []*ModelsEndpointResponse{{Models: []ModelInfo{reviewer}, ETag: "etag-1"}}}
	manager := NewRemoteModelsManager(&ModelsResponse{Models: []ModelInfo{bundled, reviewer}}, endpoint)
	manager.RawModelCatalog(RefreshOnline)

	provider := manager.GetCatalogModel("codex-auto-review", nil)
	if provider.Origin != ModelOriginProvider || provider.Model.Slug != "codex-auto-review" {
		t.Fatalf("provider lookup = %#v", provider)
	}
	namespaced := manager.GetCatalogModel("acme/codex-auto-review", nil)
	if namespaced.Origin != ModelOriginProvider || namespaced.Model.Slug != "acme/codex-auto-review" {
		t.Fatalf("namespaced provider lookup = %#v", namespaced)
	}
	if got := manager.GetCatalogModel("bundled-model", nil); got.Origin != ModelOriginBundled {
		t.Fatalf("bundled lookup origin = %q", got.Origin)
	}
	if got := manager.GetCatalogModel("missing-model", config); got.Origin != ModelOriginSynthetic {
		t.Fatalf("missing lookup origin = %q, want synthetic", got.Origin)
	}
}

// Rust #58361: a disk-cache reload keeps the provider origin for entries the
// provider advertised, even though the merged catalog also contains bundled
// models.
func TestGetCatalogModelKeepsOriginAfterCacheReloadLikeRust(t *testing.T) {
	home := t.TempDir()
	reviewer := ModelInfo{Slug: "codex-auto-review", DisplayName: "Auto review", Visibility: VisibilityVisible, SupportedInAPI: true}
	bundled := ModelInfo{Slug: "bundled-model", DisplayName: "Bundled", Visibility: VisibilityVisible, SupportedInAPI: true}
	endpoint := &recordingModelsEndpoint{responses: []*ModelsEndpointResponse{{Models: []ModelInfo{reviewer}, ETag: "etag-1"}}}
	first := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		ModelCatalog: &ModelsResponse{Models: []ModelInfo{bundled, reviewer}},
		Endpoint:     endpoint,
		Identity:     "identity-a",
	})
	first.ConfigureCache(home)
	first.RawModelCatalog(RefreshOnline)

	restarted := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		ModelCatalog: &ModelsResponse{Models: []ModelInfo{bundled, reviewer}},
		Endpoint:     endpoint,
		Identity:     "identity-a",
	})
	restarted.ConfigureCache(home)
	restarted.RawModelCatalog(RefreshOffline)
	if got := restarted.GetCatalogModel("codex-auto-review", nil); got.Origin != ModelOriginProvider {
		t.Fatalf("cached provider origin = %q, want provider", got.Origin)
	}
	if got := restarted.GetCatalogModel("bundled-model", nil); got.Origin != ModelOriginBundled {
		t.Fatalf("cached bundled origin = %q, want bundled", got.Origin)
	}
}
