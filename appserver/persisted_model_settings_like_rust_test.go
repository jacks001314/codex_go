package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

func appendPersistedModelSnapshot(t *testing.T, router *Router, record *session.Record, settings rollout.ThreadSettingsApplied) {
	t.Helper()
	recorder, err := rollout.Resume(router.threadRolloutPath(record))
	if err != nil {
		t.Fatalf("rollout.Resume() error = %v", err)
	}
	defer recorder.Close()
	if err := recorder.AppendThreadSettingsAppliedSnapshot(string(record.ID), settings, fixedTime().Add(time.Minute)); err != nil {
		t.Fatalf("AppendThreadSettingsAppliedSnapshot() error = %v", err)
	}
}

// Mirrors Rust #55923: a cold resume restores the thread-owned service tier,
// reasoning summary, and personality unless the caller overrides them.
func TestColdResumeRestoresPersistedModelSettingsLikeRust(t *testing.T) {
	router, record, cwd := newPersistedResumeSettingsFixture(t)
	appendPersistedModelSnapshot(t, router, record, rollout.ThreadSettingsApplied{
		CWD:              cwd,
		ServiceTier:      modelSettingPtr("flex"),
		ReasoningSummary: modelSettingPtr("concise"),
		Personality:      modelSettingPtr("friendly"),
	})

	response, err := router.handleThreadResume(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{
		ThreadID: string(record.ID),
	}))
	if err != nil {
		t.Fatalf("thread/resume error = %v", err)
	}
	if response.ServiceTier == nil || *response.ServiceTier != "flex" {
		t.Fatalf("resume service tier = %#v, want flex", response.ServiceTier)
	}
	updated, err := router.store.Read(record.ID, true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	overrides := threadRecordConfigOverrides(updated)
	if got, _ := threadConfigStringValue(overrides, "service_tier"); got != "flex" {
		t.Fatalf("persisted service_tier = %q, want flex", got)
	}
	if got, _ := threadConfigStringValue(overrides, "model_reasoning_summary"); got != "concise" {
		t.Fatalf("persisted reasoning summary = %q, want concise", got)
	}
	if got, _ := threadConfigStringValue(overrides, "personality"); got != "friendly" {
		t.Fatalf("persisted personality = %q, want friendly", got)
	}
	lines, _, err := rollout.Load(router.threadRolloutPath(updated))
	if err != nil {
		t.Fatalf("rollout.Load() error = %v", err)
	}
	applied, ok := rollout.LatestPersistedOwnedThreadSettingsApplied(string(record.ID), lines)
	if !ok || applied.ServiceTier == nil || *applied.ServiceTier != "flex" ||
		applied.ReasoningSummary == nil || *applied.ReasoningSummary != "concise" ||
		applied.Personality == nil || *applied.Personality != "friendly" {
		t.Fatalf("resume checkpoint = %#v", applied)
	}

	// Explicit request overrides win over the restored snapshot. The second
	// resume runs through a fresh router, matching Rust's cold-resume test which
	// builds a new app server for every iteration. An unsupported tier is still
	// retained for persistence even though the effective response filters it out.
	summary := "detailed"
	personality := "none"
	priority := "priority"
	coldRouter := NewRouter(router.store)
	override, err := coldRouter.handleThreadResume(requestWithParams(t, IntID(2), MethodThreadResume, ThreadResumeParams{
		ThreadID:    string(record.ID),
		ServiceTier: &priority,
		Config: map[string]any{
			"model_reasoning_summary": summary,
			"personality":             personality,
		},
	}))
	if err != nil {
		t.Fatalf("thread/resume with overrides error = %v", err)
	}
	if override.ServiceTier != nil {
		t.Fatalf("unsupported resume service tier = %#v, want nil", override.ServiceTier)
	}
	updated, err = router.store.Read(record.ID, true, true)
	if err != nil {
		t.Fatalf("Read() after overrides error = %v", err)
	}
	overrides = threadRecordConfigOverrides(updated)
	if got, _ := threadConfigStringValue(overrides, "service_tier"); got != priority {
		t.Fatalf("persisted selected tier = %q, want %s", got, priority)
	}
	if got, _ := threadConfigStringValue(overrides, "model_reasoning_summary"); got != summary {
		t.Fatalf("persisted selected summary = %q, want %s", got, summary)
	}
	if got, _ := threadConfigStringValue(overrides, "personality"); got != personality {
		t.Fatalf("persisted selected personality = %q, want %s", got, personality)
	}
	lines, _, err = rollout.Load(router.threadRolloutPath(updated))
	if err != nil {
		t.Fatalf("rollout.Load() after overrides error = %v", err)
	}
	applied, ok = rollout.LatestPersistedOwnedThreadSettingsApplied(string(record.ID), lines)
	if !ok || applied.ServiceTier == nil || *applied.ServiceTier != priority {
		t.Fatalf("selected tier checkpoint = %#v", applied)
	}
}

// Mirrors Rust #55923: the initial settings checkpoint is written before user
// input, so an interrupted first turn still persists its model selections.
func TestTurnStartSettingsCheckpointPrecedesUserInputLikeRust(t *testing.T) {
	router, record, cwd := newPersistedResumeSettingsFixture(t)
	record.Metadata.Extra["config"] = map[string]any{
		"service_tier":            "flex",
		"model_reasoning_summary": "concise",
		"personality":             "friendly",
	}
	if err := router.store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	runtimeRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: router,
		Config:       config.NewConfigService(codexHomeFromSessionStore(router.store)),
		Models:       model.NewModelService(nil),
		ThreadExtras: NewThreadExtraService(),
	})
	params := &turn.TurnStartParams{
		ThreadID: string(record.ID),
		Input:    []turn.TurnUserInput{{Type: "text", Text: "hello"}},
	}
	// turn/start checkpoints settings synchronously before the async turn
	// worker persists the accepted user input.
	if err := runtimeRouter.persistTurnStartRuntimeWorkspaceRoots(params); err != nil {
		t.Fatalf("persistTurnStartRuntimeWorkspaceRoots() error = %v", err)
	}
	if !runtimeRouter.persistRuntimeTurnPrompt(string(record.ID), "turn-2", params, fixedTime().Add(time.Minute)) {
		t.Fatal("persistRuntimeTurnPrompt() = false")
	}

	lines, _, err := rollout.Load(router.threadRolloutPath(record))
	if err != nil {
		t.Fatalf("rollout.Load() error = %v", err)
	}
	settingsIndex := -1
	promptIndex := -1
	for index, line := range lines {
		if settingsIndex == -1 && strings.Contains(string(line.Payload), `"type":"thread_settings_applied"`) {
			settingsIndex = index
		}
		if promptIndex == -1 && strings.Contains(string(line.Item), `"role":"user"`) {
			promptIndex = index
		}
	}
	if settingsIndex == -1 {
		t.Fatal("rollout has no thread_settings_applied checkpoint")
	}
	if promptIndex == -1 {
		t.Fatal("rollout has no user prompt")
	}
	if settingsIndex > promptIndex {
		t.Fatalf("settings checkpoint index %d follows user input index %d", settingsIndex, promptIndex)
	}
	applied, ok := rollout.LatestPersistedOwnedThreadSettingsApplied(string(record.ID), lines)
	if !ok || applied.ServiceTier == nil || *applied.ServiceTier != "flex" ||
		applied.ReasoningSummary == nil || *applied.ReasoningSummary != "concise" ||
		applied.Personality == nil || *applied.Personality != "friendly" {
		t.Fatalf("initial settings checkpoint = %#v (cwd=%s)", applied, cwd)
	}
}

// Mirrors Rust #55923: thread/revert carries the runtime model, service tier,
// and personality into the reconstructed thread.
func TestThreadRevertCarriesModelSettingsLikeRust(t *testing.T) {
	router, record, _ := newPersistedResumeSettingsFixture(t)
	record.Metadata.HistoryMode = string(ThreadHistoryPaginated)
	record.Metadata.Model = "old-model"
	if err := router.store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	extras := NewThreadExtraService()
	serviceTier := "flex"
	personality := "none"
	if _, err := extras.UpdateSettings(&SettingsUpdateParams{
		ThreadID:       string(record.ID),
		ServiceTier:    &ThreadExtraOptionalString{Set: true, Value: &serviceTier},
		Personality:    &personality,
		PersonalitySet: true,
		CollaborationMode: map[string]any{
			"mode":     "default",
			"settings": map[string]any{"model": "revert-model"},
		},
	}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	runtimeRouter := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: router,
		Config:       config.NewConfigService(t.TempDir()),
		Models:       model.NewModelService(nil),
		ThreadExtras: extras,
	})
	runtimeRouter.applyThreadRevertModelSettings(string(record.ID))
	updated, err := router.store.Read(record.ID, true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if updated.Metadata.Model != "revert-model" {
		t.Fatalf("reverted model = %q, want revert-model", updated.Metadata.Model)
	}
	overrides := threadRecordConfigOverrides(updated)
	if got, _ := threadConfigStringValue(overrides, "service_tier"); got != serviceTier {
		t.Fatalf("reverted service_tier = %q, want %s", got, serviceTier)
	}
	if got, _ := threadConfigStringValue(overrides, "personality"); got != personality {
		t.Fatalf("reverted personality = %q, want %s", got, personality)
	}
	runtimeRouter.checkpointThreadSettings(string(record.ID))
	lines, _, err := rollout.Load(router.threadRolloutPath(updated))
	if err != nil {
		t.Fatalf("rollout.Load() error = %v", err)
	}
	applied, ok := rollout.LatestPersistedOwnedThreadSettingsApplied(string(record.ID), lines)
	if !ok || applied.ServiceTier == nil || *applied.ServiceTier != serviceTier ||
		applied.Personality == nil || *applied.Personality != personality {
		t.Fatalf("revert checkpoint = %#v", applied)
	}
}

// Mirrors Rust #55923: a turn request keeps the selected flex tier even when
// the model catalog does not advertise it, while other unsupported tiers are
// filtered from the request.
func TestTurnServiceTierKeepsSelectedFlexLikeRust(t *testing.T) {
	models := model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
		Slug:           "gpt-test",
		DisplayName:    "GPT Test",
		Visibility:     model.VisibilityVisible,
		SupportedInAPI: true,
		ServiceTiers:   []string{"priority"},
	}}}))
	router := NewRuntimeRouter(RuntimeServices{Models: models})
	cfg := &config.Config{Values: map[string]any{}}

	flex := "flex"
	if got := router.appServiceTierForTurn(cfg, &turn.TurnStartParams{ServiceTier: &flex}, "gpt-test"); got != "flex" {
		t.Fatalf("thread flex tier = %q, want flex", got)
	}
	priority := "priority"
	if got := router.appServiceTierForTurn(cfg, &turn.TurnStartParams{ServiceTier: &priority}, "gpt-test"); got != "priority" {
		t.Fatalf("thread priority tier = %q, want priority", got)
	}
	unsupported := "priority-unsupported"
	if got := router.appServiceTierForTurn(cfg, &turn.TurnStartParams{ServiceTier: &unsupported}, "gpt-test"); got != "" {
		t.Fatalf("unsupported thread tier = %q, want empty", got)
	}
	if got := router.appServiceTierForTurn(cfg, &turn.TurnStartParams{ServiceTierForTurn: &flex}, "gpt-test"); got != "flex" {
		t.Fatalf("per-turn flex tier = %q, want flex", got)
	}
}

// Mirrors Rust #55923: the live settings snapshot stores the parsed service
// tier, so the wire alias "fast" is persisted as "priority".
func TestSettingsUpdateNormalizesFastTierLikeRust(t *testing.T) {
	extras := NewThreadExtraService()
	fast := "fast"
	if _, err := extras.UpdateSettings(&SettingsUpdateParams{
		ThreadID:    "thread-1",
		ServiceTier: &ThreadExtraOptionalString{Set: true, Value: &fast},
	}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	settings := extras.Settings("thread-1")
	if settings.ServiceTier == nil || *settings.ServiceTier != "priority" {
		t.Fatalf("stored service tier = %#v, want priority", settings.ServiceTier)
	}
	if _, err := extras.UpdateSettings(&SettingsUpdateParams{
		ThreadID:    "thread-1",
		ServiceTier: &ThreadExtraOptionalString{Set: true},
	}); err != nil {
		t.Fatalf("UpdateSettings() error = %v", err)
	}
	settings = extras.Settings("thread-1")
	if settings.ServiceTier == nil || *settings.ServiceTier != "default" {
		t.Fatalf("cleared service tier = %#v, want default", settings.ServiceTier)
	}
}
