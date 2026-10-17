package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Rust session::step_settings: a mid-turn turn/settings/update reaches the
// following sampling step, so the step's model, reasoning effort, and request
// metadata describe the updated settings.
func TestTurnStepSettingsProviderReflectsMidTurnUpdatesLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	params := &turn.TurnStartParams{ThreadID: "thread-1", Model: "gpt-initial"}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	if !router.threads.UpdateTurn("thread-1", "turn-1", func(active *activeRuntimeTurn) {
		active.RunConfig = &appTurnRunConfig{
			Model:           "gpt-initial",
			ReasoningEffort: "low",
			ClientMetadata: map[string]string{
				codexapi.ClientCodexTurnMetadataHeader: `{"thread_id":"thread-1","model":"gpt-initial","reasoning_effort":"low","codex_version":"1.2.3"}`,
			},
		}
	}) {
		t.Fatal("UpdateTurn() did not find the registered turn")
	}
	provider := router.turnStepSettingsProvider("thread-1", "turn-1")
	before := provider()
	if before == nil || before.Model != "gpt-initial" || before.ReasoningEffort != "low" {
		t.Fatalf("initial step settings = %#v", before)
	}
	if metadata := before.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]; !strings.Contains(metadata, `"model":"gpt-initial"`) {
		t.Fatalf("initial step metadata = %q", metadata)
	}

	model, effort := "gpt-updated", "high"
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Model:    &model,
		Effort:   &effort,
	})
	after := provider()
	if after.Model != "gpt-updated" || after.ReasoningEffort != "high" {
		t.Fatalf("updated step settings = %#v", after)
	}
	metadata := after.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]
	if !strings.Contains(metadata, `"model":"gpt-updated"`) || !strings.Contains(metadata, `"reasoning_effort":"high"`) {
		t.Fatalf("updated step metadata = %q", metadata)
	}
	// Every other metadata entry survives the refresh.
	if !strings.Contains(metadata, `"codex_version":"1.2.3"`) || !strings.Contains(metadata, `"thread_id":"thread-1"`) {
		t.Fatalf("step metadata lost entries: %q", metadata)
	}

	// Clearing the effort removes the key, like Rust's ExecutionMetadata.
	cleared := ""
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Effort:   &cleared,
	})
	if metadata := provider().ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]; strings.Contains(metadata, "reasoning_effort") {
		t.Fatalf("cleared step metadata = %q", metadata)
	}

	// A turn that is no longer active reports nothing.
	if _, ok := router.threads.ConsumeTurn("thread-1", "turn-1", true); !ok {
		t.Fatal("ConsumeTurn() did not find the active turn")
	}
	if settings := provider(); settings != nil {
		t.Fatalf("inactive turn step settings = %#v", settings)
	}
}

// Mirrors Rust #50128: current_turn_model returns the model selected for the
// named running turn's next step, ignoring other turn ids and reporting nothing
// once the turn stops running.
func TestCurrentTurnModelLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	params := &turn.TurnStartParams{ThreadID: "thread-1", Model: "gpt-initial"}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	if !router.threads.UpdateTurn("thread-1", "turn-1", func(active *activeRuntimeTurn) {
		active.RunConfig = &appTurnRunConfig{Model: "gpt-initial"}
	}) {
		t.Fatal("UpdateTurn() did not find the registered turn")
	}
	if got := router.currentTurnModel("thread-1", "other-turn"); got != "" {
		t.Fatalf("currentTurnModel(mismatched) = %q, want empty", got)
	}
	if got := router.currentTurnModel("thread-1", "turn-1"); got != "gpt-initial" {
		t.Fatalf("currentTurnModel(initial) = %q, want gpt-initial", got)
	}
	updated := "gpt-updated"
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Model:    &updated,
	})
	if got := router.currentTurnModel("thread-1", "turn-1"); got != "gpt-updated" {
		t.Fatalf("currentTurnModel(after update) = %q, want gpt-updated", got)
	}
	if _, ok := router.threads.ConsumeTurn("thread-1", "turn-1", true); !ok {
		t.Fatal("ConsumeTurn() did not find the active turn")
	}
	if got := router.currentTurnModel("thread-1", "turn-1"); got != "" {
		t.Fatalf("currentTurnModel(completed) = %q, want empty", got)
	}
}

func TestTurnStepInputItemsResolveTokenBudgetFromEachActiveModelLikeRust(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "models.json")
	catalog := model.ModelsResponse{Models: []model.ModelInfo{
		tokenBudgetStepModel("gpt-initial", 272_000, "Use initial guidance."),
		tokenBudgetStepModel("gpt-destination", 190_000, "Use destination guidance."),
		{
			Slug:             "gpt-no-window",
			BaseInstructions: "no-window instructions",
			ModelMessages: &model.ModelMessages{TokenBudget: &model.ModelTokenBudgetConfig{
				ReminderThresholdTokens:         2_000,
				ReminderMessageTemplate:         "{n_remaining} tokens remain.",
				GuidanceMessage:                 "Use no-window guidance.",
				AutoCompactFallbackPrompt:       "Save state.",
				AutoCompactFallbackBufferTokens: 4_000,
			}},
		},
	}}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Values: map[string]any{
		"model_catalog_json": catalogPath,
		"features": map[string]any{
			"token_budget": map[string]any{"enabled": true},
		},
	}}
	configured, err := cfg.TokenBudgetConfig()
	if err != nil {
		t.Fatal(err)
	}
	initialInfo := model.NewStaticModelsManager(catalog).GetModelInfo("gpt-initial", nil)
	if initialInfo.Slug == "" {
		t.Fatal("initial model info is missing")
	}
	useModelDefaults := cfg.UseModelTokenBudgetDefaults(modelTokenBudgetDefaults(&initialInfo), true)
	if !useModelDefaults {
		t.Fatal("model defaults should be used when only activation gates are configured")
	}

	store := session.NewStore(dir)
	now := time.Now().UTC()
	threadID := session.ThreadID("thread-step-token-budget")
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy)},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })
	runConfig := &appTurnRunConfig{
		Config:                      cfg,
		ConfiguredTokenBudget:       configured,
		UseModelTokenBudgetDefaults: useModelDefaults,
		TokenBudgetFeatureEnabled:   true,
	}
	provider := router.turnStepInputItemsProvider(string(threadID), runConfig)
	if provider == nil {
		t.Fatal("step input provider is nil")
	}

	expectStepGuidance := func(modelID string, want string) {
		t.Helper()
		items, err := provider(nil, 0, modelID, "")
		if err != nil {
			t.Fatalf("step %s: %v", modelID, err)
		}
		texts := guidanceTexts(items)
		if want == "" {
			if len(texts) != 0 {
				t.Fatalf("step %s guidance = %#v, want none", modelID, texts)
			}
			return
		}
		if len(texts) != 1 || !strings.Contains(texts[0], want) {
			t.Fatalf("step %s guidance = %#v, want %q", modelID, texts, want)
		}
	}
	expectStepGuidance("gpt-initial", "Use initial guidance.")
	expectStepGuidance("gpt-initial", "")
	// A reload must not change the preferences captured for this turn.
	cfg.Values["features"] = map[string]any{"token_budget": map[string]any{
		"enabled":          true,
		"guidance_message": "Reloaded user guidance.",
	}}
	expectStepGuidance("gpt-destination", "Use destination guidance.")
	expectStepGuidance("gpt-destination", "")
	expectStepGuidance("gpt-no-window", "The previously provided context-window guidance no longer applies.")
}

func TestTurnStepInputItemsPreserveExplicitTokenBudgetSettingsLikeRust(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "models.json")
	catalog := model.ModelsResponse{Models: []model.ModelInfo{
		tokenBudgetStepModel("gpt-initial", 272_000, "Model initial guidance."),
		tokenBudgetStepModel("gpt-destination", 190_000, "Model destination guidance."),
	}}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Values: map[string]any{
		"model_catalog_json": catalogPath,
		"features": map[string]any{"token_budget": map[string]any{
			"enabled":          true,
			"guidance_message": "Explicit user guidance.",
		}},
	}}
	configured, err := cfg.TokenBudgetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasExplicitTokenBudgetSettings() {
		t.Fatal("explicit settings were not detected")
	}

	store := session.NewStore(dir)
	now := time.Now().UTC()
	threadID := session.ThreadID("thread-step-explicit-token-budget")
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy)},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })
	provider := router.turnStepInputItemsProvider(string(threadID), &appTurnRunConfig{
		Config:                    cfg,
		ConfiguredTokenBudget:     configured,
		TokenBudgetFeatureEnabled: true,
	})
	items, err := provider(nil, 1, "gpt-initial", "")
	if err != nil {
		t.Fatal(err)
	}
	if texts := guidanceTexts(items); len(texts) != 1 || !strings.Contains(texts[0], "Explicit user guidance.") {
		t.Fatalf("initial explicit guidance = %#v", texts)
	}
	items, err = provider(nil, 2, "gpt-destination", "")
	if err != nil {
		t.Fatal(err)
	}
	if texts := guidanceTexts(items); len(texts) != 0 {
		t.Fatalf("explicit guidance should stay unchanged across a model switch: %#v", texts)
	}
}

func tokenBudgetStepModel(slug string, contextWindow int64, guidance string) model.ModelInfo {
	return model.ModelInfo{
		Slug:                          slug,
		BaseInstructions:              slug + " instructions",
		ContextWindow:                 contextWindow,
		EffectiveContextWindowPercent: 95,
		ModelMessages: &model.ModelMessages{TokenBudget: &model.ModelTokenBudgetConfig{
			ReminderThresholdTokens:         2_000,
			ReminderMessageTemplate:         "{n_remaining} tokens remain.",
			GuidanceMessage:                 guidance,
			AutoCompactFallbackPrompt:       "Save state.",
			AutoCompactFallbackBufferTokens: 4_000,
		}},
	}
}

func guidanceTexts(items []any) []string {
	texts := messageInputTextsForRole(items, "developer")
	out := make([]string, 0, len(texts))
	for _, text := range texts {
		if strings.HasPrefix(text, "<context_window_guidance>") {
			out = append(out, text)
		}
	}
	return out
}
