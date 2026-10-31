package appserver

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/model"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust #44242: tool execution reads the model captured for the issuing sampling
// step, so a mid-turn model switch changes image support (and original-detail
// support) instead of keeping the turn's initial model capabilities.
func TestStepViewImageCapabilitiesFollowCapturedModelLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "step-image-model", InputModalities: []string{"text", "image"}, SupportsImageDetailOriginal: true},
			{Slug: "step-text-model", InputModalities: []string{"text"}},
		}})),
	})
	t.Cleanup(func() { _ = router.Close() })

	threadID := "thread-step-view-image"
	turnID := "turn-step-view-image"
	dir := t.TempDir()
	cfg := &config.Config{Values: map[string]any{}}
	params := &turn.TurnStartParams{ThreadID: threadID, Model: "step-image-model"}
	if err := router.threads.RegisterTurn(threadID, turnID, nil, time.Now().UTC().UnixMilli(), params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}

	options := router.viewImageOptionsForTurn(cfg, params, dir, threadID, turnID)
	if options == nil || options.StepSettings == nil {
		t.Fatalf("viewImageOptionsForTurn() = %#v", options)
	}
	if settings, ok := options.StepSettings(); !ok || !settings.SupportsImageInput || !settings.CanRequestOriginalDetail {
		t.Fatalf("image model step settings = %#v, ok=%v", settings, ok)
	}

	if !router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{ThreadID: threadID, TurnID: turnID, Model: stringPtr("step-text-model")}) {
		t.Fatal("mid-turn model switch was not applied")
	}
	settings, ok := options.StepSettings()
	if !ok || settings.SupportsImageInput || settings.CanRequestOriginalDetail {
		t.Fatalf("text model step settings = %#v, ok=%v", settings, ok)
	}
	if _, err := tool.NewViewImageHandler(*options).Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"path":"sample.png"}`}}); err == nil ||
		!strings.Contains(err.Error(), "view_image is not allowed because you do not support image inputs") {
		t.Fatalf("view_image on a text-only step model error = %v", err)
	}

	if !router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{ThreadID: threadID, TurnID: turnID, Model: stringPtr("step-image-model")}) {
		t.Fatal("mid-turn model switch back was not applied")
	}
	writeTinyPNG(t, dir)
	out, err := tool.NewViewImageHandler(*options).Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"path":"sample.png","detail":"original"}`}})
	if err != nil {
		t.Fatalf("view_image after switching back error = %v", err)
	}
	if out.Data["detail"] != "original" {
		t.Fatalf("view_image detail = %#v, want original", out.Data["detail"])
	}
}

// Rust #44242: the MCP turn-metadata document describes the issuing step, so its
// model-owned node-REPL flags follow a mid-turn model switch.
func TestStepMCPMetadataCarriesCapturedModelNodeReplFlagsLikeRust(t *testing.T) {
	metadata := map[string]string{
		codexapi.ClientCodexTurnMetadataHeader: `{"model":"step-a","reasoning_effort":"low","node_repl_disabled":false,"node_repl_auto_review_required":false}`,
	}
	info := &model.ModelInfo{NodeReplDisabled: true, NodeReplAutoReviewRequired: true}
	document := decodeCompactionTurnMetadata(t, stepTurnClientMetadata(metadata, info, "step-b", "high")[codexapi.ClientCodexTurnMetadataHeader])
	if document[codexapi.ModelKey] != "step-b" || document[codexapi.ReasoningEffortKey] != "high" {
		t.Fatalf("step metadata model/effort = %#v", document)
	}
	if document[codexapi.NodeReplDisabledKey] != true || document[codexapi.NodeReplAutoReviewRequiredKey] != true {
		t.Fatalf("step metadata node-REPL flags = %#v", document)
	}

	unchanged := decodeCompactionTurnMetadata(t, stepTurnClientMetadata(metadata, nil, "", "")[codexapi.ClientCodexTurnMetadataHeader])
	if unchanged[codexapi.NodeReplDisabledKey] != false || unchanged[codexapi.NodeReplAutoReviewRequiredKey] != false {
		t.Fatalf("metadata without model info changed the node-REPL flags = %#v", unchanged)
	}

	router := NewRuntimeRouter(RuntimeServices{
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "step-repl-off", InputModalities: []string{"text"}},
			{Slug: "step-repl-on", InputModalities: []string{"text"}, NodeReplDisabled: true},
		}})),
	})
	t.Cleanup(func() { _ = router.Close() })
	threadID := "thread-step-node-repl"
	turnID := "turn-step-node-repl"
	if err := router.threads.RegisterTurn(threadID, turnID, nil, time.Now().UTC().UnixMilli(), &turn.TurnStartParams{ThreadID: threadID, Model: "step-repl-off"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	if !router.threads.UpdateTurn(threadID, turnID, func(active *activeRuntimeTurn) {
		active.RunConfig = &appTurnRunConfig{Model: "step-repl-off"}
	}) {
		t.Fatal("UpdateTurn() did not find the registered turn")
	}
	stepDocument := func() map[string]any {
		t.Helper()
		client := router.activeTurnClientMetadata(threadID, turnID, nil)
		return decodeCompactionTurnMetadata(t, client[codexapi.ClientCodexTurnMetadataHeader])
	}
	if document := stepDocument(); document[codexapi.ModelKey] != "step-repl-off" || document[codexapi.NodeReplDisabledKey] != false {
		t.Fatalf("first step metadata = %#v", document)
	}
	if !router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{ThreadID: threadID, TurnID: turnID, Model: stringPtr("step-repl-on")}) {
		t.Fatal("mid-turn model switch was not applied")
	}
	if document := stepDocument(); document[codexapi.ModelKey] != "step-repl-on" || document[codexapi.NodeReplDisabledKey] != true {
		t.Fatalf("second step metadata = %#v", document)
	}
}

func writeTinyPNG(t *testing.T, dir string) {
	t.Helper()
	const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	decoded, err := base64.StdEncoding.DecodeString(tinyPNG)
	if err != nil {
		t.Fatalf("decode png = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample.png"), decoded, 0o600); err != nil {
		t.Fatalf("write png = %v", err)
	}
}
