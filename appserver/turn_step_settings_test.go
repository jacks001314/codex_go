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
			Model:            "gpt-initial",
			ReasoningEffort:  "low",
			ReasoningSummary: "concise",
			ServiceTier:      "",
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
	if after.Model != "gpt-updated" || after.ReasoningEffort != "high" || after.ReasoningSummary != "concise" {
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
	environments := []map[string]any{{"environmentId": "next", "cwd": t.TempDir()}}
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		Environments: environments,
	})
	active := router.activeRuntimeTurnStateSnapshot("thread-1", "turn-1")
	if active == nil || active.Params == nil || len(active.Params.Environments) != 1 ||
		selectionEnvironmentID(active.Params.Environments[0]) != "next" {
		t.Fatalf("active environments = %#v", active)
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

	summary := "detailed"
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Summary:  &summary,
	})
	if got := provider().ReasoningSummary; got != summary {
		t.Fatalf("updated reasoning summary = %q, want %q", got, summary)
	}

	// A turn that is no longer active reports nothing.
	if _, ok := router.threads.ConsumeTurn("thread-1", "turn-1", true); !ok {
		t.Fatal("ConsumeTurn() did not find the active turn")
	}
	if settings := provider(); settings != nil {
		t.Fatalf("inactive turn step settings = %#v", settings)
	}
}

// Mirrors Rust #40656: the response distinguishes an applied update from a
// target turn that is no longer live.
func TestTurnSettingsUpdateReportsAppliedOrUnavailableLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[features]\nstep_model_switching = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	model := "gpt-updated"
	params := turn.TurnSettingsUpdateParams{ThreadID: "thread-1", TurnID: "turn-1", Model: &model}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	request := &Request{
		JSONRPC: "2.0",
		ID:      IntID(1),
		Method:  MethodTurnSettingsUpdate,
		Params:  raw,
	}
	unavailable, err := router.handleTurnSettingsUpdate(request)
	if err != nil {
		t.Fatalf("unavailable update error = %v", err)
	}
	if unavailable.Status != turn.TurnSettingsUpdateStatusTargetUnavailable {
		t.Fatalf("unavailable status = %q, want %q", unavailable.Status, turn.TurnSettingsUpdateStatusTargetUnavailable)
	}

	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, &turn.TurnStartParams{ThreadID: "thread-1"}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	applied, err := router.handleTurnSettingsUpdate(request)
	if err != nil {
		t.Fatalf("applied update error = %v", err)
	}
	if applied.Status != turn.TurnSettingsUpdateStatusApplied {
		t.Fatalf("applied status = %q, want %q", applied.Status, turn.TurnSettingsUpdateStatusApplied)
	}
	encoded, err := json.Marshal(applied)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"status":"applied"}` {
		t.Fatalf("applied response = %s, want the camelCase status", encoded)
	}
	active := router.activeRuntimeTurnStateSnapshot("thread-1", "turn-1")
	if active == nil || active.Params == nil || active.Params.Model != model {
		t.Fatalf("active turn settings = %#v, want model %q", active, model)
	}
}

func TestTurnSettingsUpdateRequiresStepModelSwitchingLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	model := "gpt-updated"
	raw, err := json.Marshal(turn.TurnSettingsUpdateParams{ThreadID: "thread-1", TurnID: "turn-1", Model: &model})
	if err != nil {
		t.Fatal(err)
	}
	_, err = router.handleTurnSettingsUpdate(&Request{
		JSONRPC: "2.0",
		ID:      IntID(1),
		Method:  MethodTurnSettingsUpdate,
		Params:  raw,
	})
	if err == nil || !strings.Contains(err.Error(), "step_model_switching") {
		t.Fatalf("error = %v, want the step_model_switching feature rejection", err)
	}
}

// Mirrors Rust #47970: reads of the running turn's environment selections
// reflect mid-turn settings updates, while the turn-start snapshot stays intact
// and completed turns report no selection.
func TestCurrentTurnEnvironmentSelectionsReflectUpdatesLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	initial := []map[string]any{{"environmentId": "initial", "cwd": t.TempDir()}}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, &turn.TurnStartParams{
		ThreadID:     "thread-1",
		Environments: initial,
	}); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	if got := router.currentTurnEnvironmentSelections("thread-1", "turn-1"); len(got) != 1 || selectionEnvironmentID(got[0]) != "initial" {
		t.Fatalf("initial current selections = %#v", got)
	}

	updated := []map[string]any{{"environmentId": "updated", "cwd": t.TempDir()}}
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		Environments: updated,
	})
	if got := router.currentTurnEnvironmentSelections("thread-1", "turn-1"); len(got) != 1 || selectionEnvironmentID(got[0]) != "updated" {
		t.Fatalf("updated current selections = %#v", got)
	}
	if selectionEnvironmentID(initial[0]) != "initial" {
		t.Fatalf("turn-start snapshot was mutated: %#v", initial)
	}
	if got := router.currentTurnEnvironmentSelections("thread-1", "other-turn"); got != nil {
		t.Fatalf("mismatched turn selections = %#v, want nil", got)
	}
	if _, ok := router.threads.ConsumeTurn("thread-1", "turn-1", true); !ok {
		t.Fatal("ConsumeTurn() did not find the active turn")
	}
	if got := router.currentTurnEnvironmentSelections("thread-1", "turn-1"); got != nil {
		t.Fatalf("completed turn selections = %#v, want nil", got)
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

func TestTurnStepSettingsRefilterServiceTierForUpdatedModelLikeRust(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "models.json")
	catalog := model.ModelsResponse{Models: []model.ModelInfo{
		{Slug: "gpt-priority", ServiceTiers: []string{"priority"}},
		{Slug: "gpt-no-tier"},
	}}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{})
	tier := "priority"
	params := &turn.TurnStartParams{
		ThreadID:       "thread-step-tier",
		Model:          "gpt-priority",
		ServiceTier:    &tier,
		ServiceTierSet: true,
		Config: map[string]any{
			"model_catalog_json": catalogPath,
			"features": map[string]any{
				"fast_mode": map[string]any{"enabled": true},
			},
		},
	}
	if err := router.threads.RegisterTurn(params.ThreadID, "turn-step-tier", nil, 0, params); err != nil {
		t.Fatal(err)
	}
	if !router.threads.UpdateTurn(params.ThreadID, "turn-step-tier", func(active *activeRuntimeTurn) {
		active.RunConfig = &appTurnRunConfig{Model: "gpt-priority", ServiceTier: "priority"}
	}) {
		t.Fatal("UpdateTurn() did not find the registered turn")
	}
	provider := router.turnStepSettingsProvider(params.ThreadID, "turn-step-tier")
	if got := provider().ServiceTier; got != "priority" {
		t.Fatalf("initial service tier = %q, want priority", got)
	}
	modelID := "gpt-no-tier"
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID: params.ThreadID,
		TurnID:   "turn-step-tier",
		Model:    &modelID,
	})
	if got := provider().ServiceTier; got != "" {
		t.Fatalf("unsupported model service tier = %q, want empty", got)
	}
	modelID = "gpt-priority"
	router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID: params.ThreadID,
		TurnID:   "turn-step-tier",
		Model:    &modelID,
	})
	if got := provider().ServiceTier; got != "priority" {
		t.Fatalf("restored model service tier = %q, want priority", got)
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
	provider := router.turnStepInputItemsProvider(string(threadID), "turn-step-token-budget", runConfig)
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
	provider := router.turnStepInputItemsProvider(string(threadID), "turn-step-explicit-token-budget", &appTurnRunConfig{
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
