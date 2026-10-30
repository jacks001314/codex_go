package model

import "testing"

// TestRefreshInMemoryUsesInMemoryCatalogLikeRust covers Rust #57789's
// RefreshStrategy::InMemory: the catalog is read from memory without loading the
// disk cache or fetching from the network, so a reviewer recovered by an
// explicit refresh survives a failed cache write.
func TestRefreshInMemoryUsesInMemoryCatalogLikeRust(t *testing.T) {
	endpoint := &recordingModelsEndpoint{responses: []*ModelsEndpointResponse{{
		Models: []ModelInfo{{
			Slug:           "remote",
			DisplayName:    "Remote",
			Visibility:     VisibilityVisible,
			SupportedInAPI: true,
		}},
		ETag: "etag-1",
	}}}
	manager := NewRemoteModelsManager(&ModelsResponse{Models: []ModelInfo{{
		Slug:           "bundled",
		DisplayName:    "Bundled",
		Visibility:     VisibilityVisible,
		SupportedInAPI: true,
		Priority:       10,
	}}}, endpoint)

	if models := manager.ListModels(RefreshInMemory); len(models) != 1 || models[0].Model != "bundled" || endpoint.calls != 0 {
		t.Fatalf("in-memory models = %#v, calls = %d, want the bundled catalog with no fetch", models, endpoint.calls)
	}

	// An explicit refresh keeps the fetched catalog in memory even though the
	// next lookup never reloads the cache or fetches again.
	manager.ListModels(RefreshOnline)
	calls := endpoint.calls
	if calls != 1 {
		t.Fatalf("calls after explicit refresh = %d, want 1", calls)
	}
	models := manager.ListModels(RefreshInMemory)
	if endpoint.calls != calls {
		t.Fatalf("in-memory lookup fetched again: calls = %d, want %d", endpoint.calls, calls)
	}
	found := false
	for _, preset := range models {
		if preset.Model == "remote" {
			found = true
		}
	}
	if !found {
		t.Fatalf("in-memory models = %#v, want the refreshed remote catalog", models)
	}
}
