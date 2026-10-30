package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

func stepWorldStateCatalog(t *testing.T, models ...model.ModelInfo) *config.Config {
	t.Helper()
	catalogPath := filepath.Join(t.TempDir(), "models.json")
	data, err := json.Marshal(model.ModelsResponse{Models: models})
	if err != nil {
		t.Fatalf("Marshal catalog error = %v", err)
	}
	if err := os.WriteFile(catalogPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile catalog error = %v", err)
	}
	return &config.Config{Values: map[string]any{"model_catalog_json": catalogPath}}
}

func stepWorldStateModel(slug string, window int64) model.ModelInfo {
	return model.ModelInfo{
		Slug:             slug,
		DisplayName:      slug,
		Visibility:       model.VisibilityList,
		SupportedInAPI:   true,
		ContextWindow:    window,
		BaseInstructions: slug + " base instructions",
		ModelMessages: &model.ModelMessages{
			CollaborationModes: &model.CollaborationModeMessages{
				Default: stringPtr(slug + " collaboration instructions"),
			},
			MultiAgent: &model.MultiAgentMessages{
				Mode: &model.MultiAgentModeMessages{Explicit: stringPtr(slug + " explicit policy")},
				Role: &model.MultiAgentRoleMessages{Root: stringPtr(slug + " root role hint")},
			},
		},
	}
}

func registerStepWorldStateTurn(t *testing.T, router *RuntimeRouter, threadID string, params *turn.TurnStartParams) {
	t.Helper()
	if err := router.threads.RegisterTurn(threadID, "turn-1", nil, time.Now().UnixMilli(), params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
}

// Rust #44200 builds the collaboration mode, approval, and multi-agent sections
// from the settings captured for the sampling step, so a mid-turn model switch
// refreshes the model-owned world state instead of keeping the turn's initial
// model context.
func TestTurnStepWorldStateUsesCapturedModelLikeRust(t *testing.T) {
	cfg := stepWorldStateCatalog(t, stepWorldStateModel("model-a", 272_000), stepWorldStateModel("model-b", 190_000))

	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-step-world-state")
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy), CWD: t.TempDir()},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })

	params := &turn.TurnStartParams{
		ThreadID: string(threadID),
		Model:    "model-a",
		CollaborationMode: map[string]any{
			"mode":     "default",
			"settings": map[string]any{"model": "model-a"},
		},
	}
	registerStepWorldStateTurn(t, router, string(threadID), params)
	runConfig := &appTurnRunConfig{Model: "model-a", Config: cfg, Instructions: "model-a base instructions"}
	provider := router.turnStepInputItemsProvider(string(threadID), "turn-1", runConfig)
	if provider == nil {
		t.Fatal("step input provider is nil")
	}

	stepTexts := func(modelID string, effort string) string {
		t.Helper()
		items, err := provider(context.Background(), 0, modelID, effort)
		if err != nil {
			t.Fatalf("provider(%s) error = %v", modelID, err)
		}
		return strings.Join(messageInputTextsForRole(items, "developer"), "\n")
	}

	first := stepTexts("model-a", "low")
	for _, want := range []string{"model-a collaboration instructions", "model-a explicit policy"} {
		if !strings.Contains(first, want) {
			t.Fatalf("initial step world state missing %q:\n%s", want, first)
		}
	}

	switched := stepTexts("model-b", "high")
	for _, want := range []string{"model-b collaboration instructions", "model-b explicit policy"} {
		if !strings.Contains(switched, want) {
			t.Fatalf("switched step world state missing %q:\n%s", want, switched)
		}
	}
	for _, stale := range []string{"model-a collaboration instructions", "model-a explicit policy"} {
		if strings.Contains(switched, stale) {
			t.Fatalf("switched step world state kept the initial model's %q:\n%s", stale, switched)
		}
	}

	again, err := provider(context.Background(), 1, "model-b", "high")
	if err != nil {
		t.Fatalf("repeated provider error = %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("unchanged step world state re-emitted sections: %#v", again)
	}

	// Switching back re-emits the initial model's sections; every replacement
	// keeps its own history item identity.
	back := stepTexts("model-a", "low")
	if !strings.Contains(back, "model-a collaboration instructions") {
		t.Fatalf("switching back did not restore the initial model's sections:\n%s", back)
	}
	record, err := store.Load(threadID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	seen := make(map[string]struct{}, len(record.Items))
	for _, item := range record.Items {
		if item.ID == "" {
			continue
		}
		if _, duplicate := seen[item.ID]; duplicate {
			t.Fatalf("duplicate world-state item id %q", item.ID)
		}
		seen[item.ID] = struct{}{}
	}
}

// Rust #44200 keeps the captured step when rebuilding the initial context for
// compaction, so a mid-turn model switch compacts against the new model's
// context window and instructions.
func TestMidTurnCompactionUsesCapturedStepModelLikeRust(t *testing.T) {
	cfg := stepWorldStateCatalog(t, stepWorldStateModel("model-a", 272_000), stepWorldStateModel("model-b", 190_000))

	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-step-compaction")
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy), CWD: t.TempDir()},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })

	params := &turn.TurnStartParams{ThreadID: string(threadID), Model: "model-a", Config: cfg.Values}
	registerStepWorldStateTurn(t, router, string(threadID), params)
	runConfig := &appTurnRunConfig{Model: "model-a", Config: cfg, Instructions: "model-a base instructions"}

	modelID, info := router.midTurnCompactionStep(string(threadID), "turn-1", runConfig)
	if modelID != "model-a" || info == nil || info.Slug != "model-a" {
		t.Fatalf("initial compaction step = %q %#v", modelID, info)
	}

	nextModel := "model-b"
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID: string(threadID),
		TurnID:   "turn-1",
		Model:    &nextModel,
	})
	modelID, info = router.midTurnCompactionStep(string(threadID), "turn-1", runConfig)
	if modelID != "model-b" || info == nil || info.Slug != "model-b" {
		t.Fatalf("switched compaction step = %q %#v, want the captured model", modelID, info)
	}

	// The roll-over policy and the replacement window follow the captured step's
	// context window, not the turn's initial model.
	policyA := router.compactPolicyForTurn("model-a", params, nil)
	policyB := router.compactPolicyForTurn(modelID, params, nil)
	if policyB.WindowTokens != int(190_000*95/100) || policyB.TokenLimit != int(190_000*9/10) {
		t.Fatalf("captured step policy = %#v, want model-b's context window", policyB)
	}
	if policyA.WindowTokens == policyB.WindowTokens {
		t.Fatalf("policy did not follow the model switch: %#v vs %#v", policyA, policyB)
	}

	instructions := router.compactionStepBaseInstructions(info, runConfig, params)
	if instructions == nil || *instructions != "model-b base instructions" {
		t.Fatalf("compaction instructions = %#v, want the captured model's instructions", instructions)
	}

	// An explicit turn override stays authoritative across the model switch.
	override := "explicit turn instructions"
	params.BaseInstructions = &override
	instructions = router.compactionStepBaseInstructions(info, runConfig, params)
	if instructions == nil || *instructions != "model-a base instructions" {
		t.Fatalf("explicit override instructions = %#v, want the turn's text", instructions)
	}
}
