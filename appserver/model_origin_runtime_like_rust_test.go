package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/model"
)

// Rust #58361 keeps CatalogModel::origin when metadata resolution applies
// config overrides. The runtime snapshot records the same provenance.
func TestRuntimeRouterCatalogModelOriginSurvivesConfigOverrideLikeRust(t *testing.T) {
	catalog := model.ModelsResponse{Models: []model.ModelInfo{{
		Slug:          "gpt-known",
		ContextWindow: 100000,
	}}}
	router := NewRuntimeRouter(RuntimeServices{Models: model.NewModelService(model.NewStaticModelsManager(catalog))})
	t.Cleanup(func() { _ = router.Close() })
	cfg := &config.Config{Values: map[string]any{"model_context_window": 4242}}

	known := router.catalogModelForAppTurn("gpt-known", cfg, nil)
	if known == nil {
		t.Fatal("catalogModelForAppTurn() = nil for a known model")
	}
	if known.Origin != model.ModelOriginBundled {
		t.Fatalf("known model origin = %q, want %q", known.Origin, model.ModelOriginBundled)
	}
	if known.Model.ContextWindow != 4242 {
		t.Fatalf("known model context window = %d, want the config override 4242", known.Model.ContextWindow)
	}
	if got := catalogModelOrigin(known); got != model.ModelOriginBundled {
		t.Fatalf("snapshot origin = %q, want %q", got, model.ModelOriginBundled)
	}

	unknown := router.catalogModelForAppTurn("gpt-not-in-catalog", cfg, nil)
	if unknown == nil {
		t.Fatal("catalogModelForAppTurn() = nil for an unknown model")
	}
	if unknown.Origin != model.ModelOriginSynthetic {
		t.Fatalf("unknown model origin = %q, want %q", unknown.Origin, model.ModelOriginSynthetic)
	}
	if got := catalogModelOrigin(unknown); got != model.ModelOriginSynthetic {
		t.Fatalf("unknown snapshot origin = %q, want %q", got, model.ModelOriginSynthetic)
	}
}
