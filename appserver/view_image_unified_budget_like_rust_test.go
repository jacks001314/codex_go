package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust's unified_image_budget_enabled requires the feature plus a model that
// either uses Responses Lite or accepts original detail; view_image then drops
// its detail argument and output hint (image_preparation.rs, view_image_spec.rs).
func TestViewImageUnifiedImageBudgetFollowsFeatureAndModelLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "original-detail-model", InputModalities: []string{"text", "image"}, SupportsImageDetailOriginal: true},
			{Slug: "plain-image-model", InputModalities: []string{"text", "image"}},
		}})),
	})
	t.Cleanup(func() { _ = router.Close() })
	cwd := t.TempDir()
	params := &turn.TurnStartParams{ThreadID: "thread-unified-budget", Model: "original-detail-model"}
	enabled := &config.Config{Values: map[string]any{"features": map[string]any{"unified_image_budget": true}}}

	options := router.viewImageOptionsForTurn(enabled, params, cwd, params.ThreadID, "turn-unified-budget")
	if options == nil || !options.UnifiedImageBudget {
		t.Fatalf("viewImageOptionsForTurn() = %#v, want the unified budget", options)
	}
	properties := tool.NewViewImageHandler(*options).Spec().InputSchema["properties"].(map[string]any)
	if _, ok := properties["detail"]; ok {
		t.Fatalf("unified view_image schema still advertises detail: %#v", properties)
	}

	disabled := router.viewImageOptionsForTurn(&config.Config{Values: map[string]any{}}, params, cwd, params.ThreadID, "turn-unified-budget")
	if disabled == nil || disabled.UnifiedImageBudget {
		t.Fatalf("viewImageOptionsForTurn(disabled) = %#v, want the legacy detail surface", disabled)
	}
	if _, ok := tool.NewViewImageHandler(*disabled).Spec().InputSchema["properties"].(map[string]any)["detail"]; !ok {
		t.Fatal("legacy view_image schema lost detail")
	}

	// A model that neither uses Responses Lite nor accepts original detail keeps
	// the detail surface even with the feature on.
	plainParams := &turn.TurnStartParams{ThreadID: "thread-plain-image", Model: "plain-image-model"}
	plain := router.viewImageOptionsForTurn(enabled, plainParams, cwd, plainParams.ThreadID, "turn-plain-image")
	if plain == nil || plain.UnifiedImageBudget {
		t.Fatalf("viewImageOptionsForTurn(plain model) = %#v, want no unified budget", plain)
	}
}
