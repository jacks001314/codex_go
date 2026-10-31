package appserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// TestTokenBudgetHistoryNotesWithoutExperimentalContextLikeRust mirrors Rust
// #47625: `features.token_budget.use_history_notes_extension`, enabled
// explicitly or through model defaults, no longer rejects a turn when the model
// does not advertise `supports_experimental_context`.
func TestTokenBudgetHistoryNotesWithoutExperimentalContextLikeRust(t *testing.T) {
	dir := t.TempDir()
	catalog, err := json.Marshal(model.ModelsResponse{Models: []model.ModelInfo{{
		Slug:                        "notes-no-experimental-context",
		DisplayName:                 "Notes Without Experimental Context",
		Visibility:                  model.VisibilityList,
		SupportedInAPI:              true,
		Priority:                    1,
		SupportsExperimentalContext: false,
		ModelMessages: &model.ModelMessages{TokenBudget: &model.ModelTokenBudgetConfig{
			Enabled:                         true,
			UseHistoryNotesExtension:        true,
			ReminderThresholdTokens:         12000,
			ReminderMessageTemplate:         "remaining: {n_remaining}",
			GuidanceMessage:                 "keep notes",
			AutoCompactFallbackPrompt:       "summarize",
			AutoCompactFallbackBufferTokens: 8000,
		}},
	}}})
	if err != nil {
		t.Fatalf("marshal model catalog: %v", err)
	}
	catalogPath := filepath.Join(dir, "models.json")
	if err := os.WriteFile(catalogPath, catalog, 0o600); err != nil {
		t.Fatalf("write model catalog: %v", err)
	}

	store := session.NewStore(filepath.Join(dir, "sessions"))
	now := time.Now().UTC()
	for _, threadID := range []string{"thread-notes-explicit", "thread-notes-defaults"} {
		record := &session.Record{
			ID:        session.ThreadID(threadID),
			SessionID: threadID,
			CreatedAt: now,
			UpdatedAt: now,
			RecencyAt: now,
			Metadata:  session.Metadata{CWD: dir},
		}
		if err := store.Create(record); err != nil {
			t.Fatalf("Create(%s) error = %v", threadID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})

	const modelID = "notes-no-experimental-context"
	turnConfig := func(features map[string]any) map[string]any {
		return map[string]any{"model_catalog_json": catalogPath, "features": features}
	}

	t.Run("explicit extension activates without experimental context", func(t *testing.T) {
		params := &turn.TurnStartParams{
			ThreadID: "thread-notes-explicit",
			Prompt:   "hello",
			Model:    modelID,
			Config: turnConfig(map[string]any{"token_budget": map[string]any{
				"enabled": true, "use_history_notes_extension": true,
			}}),
		}
		runConfig, err := router.appTurnConfig(context.Background(), params.ThreadID, "turn-1", "", params, now.UnixMilli(), nil)
		if err != nil {
			t.Fatalf("appTurnConfig() error = %v, want the turn to start", err)
		}
		if runConfig == nil || runConfig.ModelInfo == nil || runConfig.ModelInfo.SupportsExperimentalContext {
			t.Fatalf("runConfig model info = %#v, want a model without experimental context", runConfig)
		}
		if runConfig.ConfiguredTokenBudget == nil || !runConfig.ConfiguredTokenBudget.UseHistoryNotesExtension {
			t.Fatalf("ConfiguredTokenBudget = %#v, want the history-notes extension", runConfig.ConfiguredTokenBudget)
		}
		if !runConfig.TokenBudgetFeatureEnabled {
			t.Fatal("TokenBudgetFeatureEnabled = false, want the token budget active")
		}
	})

	t.Run("model defaults activate without experimental context", func(t *testing.T) {
		params := &turn.TurnStartParams{
			ThreadID: "thread-notes-defaults",
			Prompt:   "hello",
			Model:    modelID,
			Config:   turnConfig(map[string]any{}),
		}
		runConfig, err := router.appTurnConfig(context.Background(), params.ThreadID, "turn-2", "", params, now.UnixMilli(), nil)
		if err != nil {
			t.Fatalf("appTurnConfig() error = %v, want the turn to start", err)
		}
		if runConfig == nil || !runConfig.UseModelTokenBudgetDefaults {
			t.Fatalf("UseModelTokenBudgetDefaults = %#v, want the model defaults applied", runConfig)
		}
		if !runConfig.TokenBudgetFeatureEnabled {
			t.Fatal("TokenBudgetFeatureEnabled = false, want the model-default token budget active")
		}
	})
}
