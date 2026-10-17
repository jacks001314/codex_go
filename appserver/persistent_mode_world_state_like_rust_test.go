package appserver

import (
	"strings"
	"testing"

	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

const persistentModeWorldStateThreadID = "123e4567-e89b-42d3-a456-426614174140"

func newPersistentModeWorldStateRouter(t *testing.T) (*RuntimeRouter, *session.Store) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	now := fixedTime()
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(persistentModeWorldStateThreadID),
		SessionID: persistentModeWorldStateThreadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli", ModelProvider: "openai"},
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	return NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)}), store
}

func persistentModeWorldStateText(t *testing.T, item any) string {
	t.Helper()
	raw, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("item = %#v, want a rendered message", item)
	}
	return textFromInputItemContent(raw["content"])
}

func readPersistentModeSnapshot(t *testing.T, store *session.Store) persistentModeSnapshot {
	t.Helper()
	record, err := store.Read(session.ThreadID(persistentModeWorldStateThreadID), true, false)
	if err != nil {
		t.Fatalf("store.Read: %v", err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatalf("DecodeWorldState: %v", err)
	}
	snapshot, kind := decodePersistentModeWorldStateSnapshot(state.PersistentMode)
	if kind != "known" {
		t.Fatalf("persistent-mode snapshot kind = %q, want known", kind)
	}
	return snapshot
}

// Mirrors Rust #41050's world-state lifecycle: emit on first use, deduplicate
// unchanged guidance, replace changed catalog text, and retire it when the
// effort is no longer persistent.
func TestPersistentModeWorldStateLifecycleLikeRust(t *testing.T) {
	router, store := newPersistentModeWorldStateRouter(t)
	cfg := &config.Config{Values: map[string]any{}}
	params := &turn.TurnStartParams{ThreadID: persistentModeWorldStateThreadID}

	first, err := router.persistentModeWorldStateInputItem(
		persistentModeWorldStateThreadID, params, "turn-1", "persistent", &model.ModelInfo{}, cfg,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstText := persistentModeWorldStateText(t, first)
	if !strings.Contains(firstText, "<persistent_mode>") || !strings.Contains(firstText, "## Proactivity") {
		t.Fatalf("first persistent-mode fragment = %q", firstText)
	}
	if strings.Contains(firstText, contextfrag.PersistentModeReplacementNotice) {
		t.Fatalf("first fragment unexpectedly claims a replacement: %q", firstText)
	}
	firstSnapshot := readPersistentModeSnapshot(t, store)
	if firstSnapshot.Instructions == "" {
		t.Fatal("first snapshot did not record the instruction hash")
	}

	unchanged, err := router.persistentModeWorldStateInputItem(
		persistentModeWorldStateThreadID, params, "turn-2", "persistent", &model.ModelInfo{}, cfg,
	)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged != nil {
		t.Fatalf("unchanged guidance was re-emitted: %#v", unchanged)
	}

	custom := "Custom persistent guidance."
	customInfo := &model.ModelInfo{ModelMessages: &model.ModelMessages{PersistentInstructions: &custom}}
	replacement, err := router.persistentModeWorldStateInputItem(
		persistentModeWorldStateThreadID, params, "turn-3", "persistent", customInfo, cfg,
	)
	if err != nil {
		t.Fatal(err)
	}
	replacementText := persistentModeWorldStateText(t, replacement)
	if !strings.Contains(replacementText, contextfrag.PersistentModeReplacementNotice) ||
		!strings.Contains(replacementText, custom) {
		t.Fatalf("replacement fragment = %q", replacementText)
	}
	if snapshot := readPersistentModeSnapshot(t, store); snapshot == firstSnapshot {
		t.Fatal("replacement did not advance the snapshot")
	}

	removal, err := router.persistentModeWorldStateInputItem(
		persistentModeWorldStateThreadID, params, "turn-4", "low", customInfo, cfg,
	)
	if err != nil {
		t.Fatal(err)
	}
	removalText := persistentModeWorldStateText(t, removal)
	if !strings.Contains(removalText, contextfrag.PersistentModeRemovalNotice) {
		t.Fatalf("removal fragment = %q", removalText)
	}
	if snapshot := readPersistentModeSnapshot(t, store); snapshot.Instructions != "" {
		t.Fatalf("removal snapshot = %#v, want disabled state", snapshot)
	}
	if repeated, err := router.persistentModeWorldStateInputItem(
		persistentModeWorldStateThreadID, params, "turn-5", "low", customInfo, cfg,
	); err != nil || repeated != nil {
		t.Fatalf("repeated removal = %#v, err %v", repeated, err)
	}
}

func TestPersistentModeWorldStateExcludesGuardianLikeRust(t *testing.T) {
	router, store := newPersistentModeWorldStateRouter(t)
	params := &turn.TurnStartParams{
		ThreadID:             persistentModeWorldStateThreadID,
		ResponsesAPIMetadata: map[string]string{"x-openai-subagent": "guardian"},
	}
	item, err := router.persistentModeWorldStateInputItem(
		persistentModeWorldStateThreadID, params, "turn-guardian", "persistent", &model.ModelInfo{},
		&config.Config{Values: map[string]any{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if item != nil {
		t.Fatalf("guardian session received persistent-mode guidance: %#v", item)
	}
	record, err := store.Read(session.ThreadID(persistentModeWorldStateThreadID), true, false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.PersistentMode) != 0 {
		t.Fatalf("guardian session wrote persistent-mode state: %s", state.PersistentMode)
	}
}

func TestPersistentModeForkScrubLikeRust(t *testing.T) {
	if !isForkExcludedDeveloperText("<persistent_mode>\nold guidance\n</persistent_mode>") {
		t.Fatal("persistent-mode developer guidance is not fork-excluded by text")
	}
	if _, ok := forkExcludedContentItemKinds["persistent_mode.instructions"]; !ok {
		t.Fatal("persistent_mode.instructions is not fork-excluded by content kind")
	}
	item, ok := persistentModeWorldStateSessionItemForTurn("turn-1", map[string]any{
		"type": "message",
		"role": "developer",
		"content": []any{
			map[string]any{"type": "input_text", "text": "<persistent_mode>\nguidance\n</persistent_mode>"},
		},
	}, fixedTime())
	if !ok || item.ID != "persistent-mode-turn-1" {
		t.Fatalf("persistent-mode session item = %#v, ok=%v", item, ok)
	}
	if item.Data["kind"] != persistentModeWorldStateInstructionsKind || item.Data["hiddenFromThread"] != true {
		t.Fatalf("persistent-mode session metadata = %#v", item.Data)
	}
}

// The per-step provider must use the step's reasoning effort so mid-turn effort
// changes replace or retire persistent guidance.
func TestPersistentModeStepInputsFollowEffortChangesLikeRust(t *testing.T) {
	router, _ := newPersistentModeWorldStateRouter(t)
	provider := router.turnStepInputItemsProvider(
		persistentModeWorldStateThreadID,
		"turn-step",
		&appTurnRunConfig{Config: &config.Config{Values: map[string]any{}}},
	)
	if provider == nil {
		t.Fatal("step input provider is nil")
	}
	persistentText := func(items []any) string {
		for _, item := range items {
			raw, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text := textFromInputItemContent(raw["content"])
			if strings.Contains(text, "<persistent_mode>") {
				return text
			}
		}
		return ""
	}

	items, err := provider(nil, 0, "gpt-test", "persistent")
	if err != nil {
		t.Fatal(err)
	}
	if text := persistentText(items); !strings.Contains(text, "## Proactivity") {
		t.Fatalf("persistent step input = %q", text)
	}
	items, err = provider(nil, 1, "gpt-test", "persistent")
	if err != nil {
		t.Fatal(err)
	}
	if text := persistentText(items); text != "" {
		t.Fatalf("unchanged step re-emitted persistent guidance: %q", text)
	}
	items, err = provider(nil, 2, "gpt-test", "low")
	if err != nil {
		t.Fatal(err)
	}
	if text := persistentText(items); !strings.Contains(text, contextfrag.PersistentModeRemovalNotice) {
		t.Fatalf("retired step input = %q", text)
	}
}
