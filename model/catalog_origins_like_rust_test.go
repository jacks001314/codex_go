package model

import "testing"

// TestModelCatalogOriginsSurviveMergeLikeRust covers Rust #57770: provider
// entries keep their origin through a merge with the bundled catalog, an empty
// provider response restores bundled origins instead of retaining old IDs, and
// a static catalog records the origin it was built with.
func TestModelCatalogOriginsSurviveMergeLikeRust(t *testing.T) {
	reviewer := ModelInfo{Slug: "codex-auto-review", DisplayName: "Auto review", Visibility: VisibilityVisible, SupportedInAPI: true}
	bundled := ModelInfo{Slug: "bundled-model", DisplayName: "Bundled", Visibility: VisibilityVisible, SupportedInAPI: true}
	endpoint := &recordingModelsEndpoint{responses: []*ModelsEndpointResponse{
		{Models: []ModelInfo{reviewer}, ETag: "etag-1"},
		{Models: []ModelInfo{}, ETag: "etag-2"},
	}}
	manager := NewRemoteModelsManager(&ModelsResponse{Models: []ModelInfo{bundled, reviewer}}, endpoint)

	offline := manager.ModelCatalogWithOrigins(RefreshOffline)
	if endpoint.calls != 0 {
		t.Fatalf("offline lookup fetched: calls = %d", endpoint.calls)
	}
	for _, entry := range offline {
		if entry.Origin != ModelOriginBundled {
			t.Fatalf("offline origin for %q = %q, want bundled", entry.Model.Slug, entry.Origin)
		}
	}

	online := manager.ModelCatalogWithOrigins(RefreshOnline)
	if endpoint.calls != 1 {
		t.Fatalf("online lookup calls = %d, want 1", endpoint.calls)
	}
	origins := map[string]ModelOrigin{}
	for _, entry := range online {
		origins[entry.Model.Slug] = entry.Origin
	}
	if origins["codex-auto-review"] != ModelOriginProvider {
		t.Fatalf("provider origin = %q, want provider", origins["codex-auto-review"])
	}
	if origins["bundled-model"] != ModelOriginBundled {
		t.Fatalf("merged bundled origin = %q, want bundled", origins["bundled-model"])
	}

	// An empty subsequent response restores bundled origins for the retained
	// merged catalog instead of keeping the previous provider IDs.
	afterEmpty := manager.ModelCatalogWithOrigins(RefreshOnline)
	if endpoint.calls != 2 {
		t.Fatalf("empty refresh calls = %d, want 2", endpoint.calls)
	}
	for _, entry := range afterEmpty {
		if entry.Origin != ModelOriginBundled {
			t.Fatalf("origin after an empty response for %q = %q, want bundled", entry.Model.Slug, entry.Origin)
		}
	}

	// A static catalog records the origin it was built with (Rust
	// static_catalog_records_its_supplied_origin).
	for _, origin := range []ModelOrigin{ModelOriginBundled, ModelOriginProvider, ModelOriginExplicitConfig} {
		static := NewStaticModelsManagerWithOrigin(ModelsResponse{Models: []ModelInfo{reviewer}}, origin)
		catalog := static.ModelCatalogWithOrigins(RefreshOffline)
		if len(catalog) != 1 || catalog[0].Origin != origin || catalog[0].Model.Slug != reviewer.Slug {
			t.Fatalf("static catalog = %#v, want origin %q", catalog, origin)
		}
	}
}
