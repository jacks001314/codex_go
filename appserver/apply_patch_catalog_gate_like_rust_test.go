package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/turn"
)

// Rust core/src/tools/spec_plan.rs:1483 advertises apply_patch only when
// `context.model_info.apply_patch_tool_type.is_some()`: the freeform custom
// tool belongs to the model's catalog entry, not to the app-server surface.
// A provider that rejects grammars therefore fails the whole request with
// "Custom tools with a grammar format are not supported". An unset model is
// resolved to the catalog default first (core/src/session/mod.rs:638).
func TestRuntimeRouterApplyPatchRequiresModelCatalogMetadataLikeRust(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})

	t.Run("default_model", func(t *testing.T) {
		toolRouter, err := router.toolRouterForTurn(t.TempDir(), &turn.TurnStartParams{ThreadID: "thread-default-model"}, "turn-1")
		if err != nil {
			t.Fatalf("toolRouterForTurn() error = %v", err)
		}
		visible := toolSpecKeySetForTest(toolRouter.ModelVisibleSpecs())
		if !visible["apply_patch"] {
			t.Fatalf("model-visible specs = %#v, default catalog model declares apply_patch", visible)
		}
	})

	t.Run("catalog_model", func(t *testing.T) {
		toolRouter, err := router.toolRouterForTurn(t.TempDir(), &turn.TurnStartParams{
			ThreadID: "thread-catalog-model",
			Model:    "gpt-6-astra",
		}, "turn-2")
		if err != nil {
			t.Fatalf("toolRouterForTurn() error = %v", err)
		}
		visible := toolSpecKeySetForTest(toolRouter.ModelVisibleSpecs())
		if !visible["apply_patch"] {
			t.Fatalf("model-visible specs = %#v, missing apply_patch for catalog model", visible)
		}
	})

	t.Run("unknown_model", func(t *testing.T) {
		toolRouter, err := router.toolRouterForTurn(t.TempDir(), &turn.TurnStartParams{
			ThreadID: "thread-unknown-model",
			Model:    "not-in-catalog",
		}, "turn-3")
		if err != nil {
			t.Fatalf("toolRouterForTurn() error = %v", err)
		}
		visible := toolSpecKeySetForTest(toolRouter.ModelVisibleSpecs())
		if visible["apply_patch"] {
			t.Fatalf("model-visible specs = %#v, apply_patch requires catalog metadata", visible)
		}
	})
}
