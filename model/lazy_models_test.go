package model

import "testing"

func TestLazyModelsManagerFallsBackToBundledLikeRust(t *testing.T) {
	// A builder that errors or returns nil must fall back to the bundled static
	// catalog and be cached (single build attempt).
	builds := 0
	manager := NewLazyModelsManager(func() (ModelsManager, error) {
		builds++
		return nil, nil
	})
	if got := manager.ListModels(RefreshOffline); len(got) == 0 {
		t.Fatal("expected non-empty bundled fallback on nil builder result")
	}
	_ = manager.RawModelCatalog(RefreshOffline)
	if builds != 1 {
		t.Fatalf("builder called %d times, want single call cached", builds)
	}
}

func TestLazyModelsManagerCachesSuccessfulBuildLikeRust(t *testing.T) {
	builds := 0
	manager := NewLazyModelsManager(func() (ModelsManager, error) {
		builds++
		return NewStaticModelsManager(ModelsResponse{Models: []ModelInfo{{
			Slug: "gpt-test", DisplayName: "Test", SupportedInAPI: true, Visibility: VisibilityVisible,
		}}}), nil
	})
	presets := manager.ListModels(RefreshOffline)
	if builds != 1 {
		t.Fatalf("builder called %d times, want once", builds)
	}
	if len(presets) != 1 || presets[0].Model != "gpt-test" {
		t.Fatalf("ListModels = %#v, want gpt-test", presets)
	}
	// Subsequent calls reuse the cached manager.
	_ = manager.RawModelCatalog(RefreshOffline)
	if builds != 1 {
		t.Fatalf("builder called %d times after cache, want once", builds)
	}
}
