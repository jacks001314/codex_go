package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust registers view_image under `features.view_image` alone (spec_plan.rs
// `if features.enabled(Feature::ViewImage)`); the model's input modalities only
// decide whether a call can run (view_image.rs rejects a step without image
// input). A text-only model therefore still sees the tool, and a mid-turn switch
// to an image-capable model keeps working.
func TestRuntimeRouterAdvertisesViewImageForTextOnlyModelLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "text-only-step-model", InputModalities: []string{"text"}},
		}})),
	})
	t.Cleanup(func() { _ = router.Close() })
	cwd := t.TempDir()
	params := &turn.TurnStartParams{ThreadID: "thread-view-image-text", Model: "text-only-step-model"}

	options := router.viewImageOptionsForTurn(nil, params, cwd, params.ThreadID, "turn-view-image-text")
	if options == nil {
		t.Fatal("viewImageOptionsForTurn() = nil, want the feature-gated registration")
	}
	toolRouter, err := router.toolRouterForTurn(cwd, params, "turn-view-image-text")
	if err != nil {
		t.Fatalf("toolRouterForTurn() error = %v", err)
	}
	visible := toolSpecKeySetForTest(toolRouter.ModelVisibleSpecs())
	if !visible[tool.ViewImageToolName] {
		t.Fatalf("model-visible specs = %#v, missing view_image for a text-only model", visible)
	}
	if _, err := tool.NewViewImageHandler(*options).Execute(context.Background(), &tool.Invocation{
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"path":"sample.png"}`},
	}); err == nil || !strings.Contains(err.Error(), "view_image is not allowed because you do not support image inputs") {
		t.Fatalf("view_image on a text-only model error = %v", err)
	}
}

// Rust #44242 rebuilds each step's tool plan from the captured model settings,
// so the view_image `detail` argument follows the step model's original-detail
// support even though the handler itself stays registered.
func TestStepToolSurfaceRefreshesViewImageDetailLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "text-only-step-model", InputModalities: []string{"text"}},
			{Slug: "original-detail-model", InputModalities: []string{"text", "image"}, SupportsImageDetailOriginal: true},
		}})),
	})
	t.Cleanup(func() { _ = router.Close() })
	cwd := t.TempDir()
	threadID := "thread-step-tool-surface"
	turnID := "turn-step-tool-surface"
	params := &turn.TurnStartParams{ThreadID: threadID, Model: "text-only-step-model"}
	if err := router.threads.RegisterTurn(threadID, turnID, nil, time.Now().UTC().UnixMilli(), params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	toolRouter, err := router.toolRouterForTurn(cwd, params, turnID)
	if err != nil {
		t.Fatalf("toolRouterForTurn() error = %v", err)
	}
	if _, ok := viewImageSpecPropertiesForTest(t, toolRouter)["detail"]; ok {
		t.Fatalf("turn-start view_image schema already advertises detail: %#v", viewImageSpecPropertiesForTest(t, toolRouter))
	}

	refresh := router.stepToolSurfaceRefresher(toolRouter, params, turnID, cwd)
	if refresh == nil {
		t.Fatal("stepToolSurfaceRefresher() = nil")
	}
	if !router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID: threadID,
		TurnID:   turnID,
		Model:    stringPtr("original-detail-model"),
	}) {
		t.Fatal("mid-turn model switch was not applied")
	}
	refresh("original-detail-model")
	if _, ok := viewImageSpecPropertiesForTest(t, toolRouter)["detail"]; !ok {
		t.Fatalf("step view_image schema = %#v, missing detail", viewImageSpecPropertiesForTest(t, toolRouter))
	}

	writeTinyPNG(t, cwd)
	executor, ok := toolRouter.Executor(tool.PlainName(tool.ViewImageToolName))
	if !ok {
		t.Fatal("view_image executor is missing")
	}
	out, err := executor.Execute(context.Background(), &tool.Invocation{
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"path":"sample.png","detail":"original"}`},
	})
	if err != nil {
		t.Fatalf("view_image on the image-capable step error = %v", err)
	}
	if out.Data["detail"] != "original" {
		t.Fatalf("view_image detail = %#v, want original", out.Data["detail"])
	}
}

func viewImageSpecPropertiesForTest(t *testing.T, router *tool.Router) map[string]any {
	t.Helper()
	for _, spec := range router.ModelVisibleSpecs() {
		if spec.Name.Namespace != "" || spec.Name.Name != tool.ViewImageToolName {
			continue
		}
		properties, _ := spec.InputSchema["properties"].(map[string]any)
		return properties
	}
	t.Fatalf("view_image spec is missing from %#v", toolSpecKeySetForTest(router.ModelVisibleSpecs()))
	return nil
}
