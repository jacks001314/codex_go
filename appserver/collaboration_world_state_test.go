package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

func TestCollaborationModeWorldStateMatchesCatalogAndModelChanges(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-collaboration-world-state")
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy)},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })

	defaultText := "catalog default instructions"
	planText := "catalog plan instructions"
	info := &model.ModelInfo{Slug: "catalog-model-a", ModelMessages: &model.ModelMessages{
		CollaborationModes: &model.CollaborationModeMessages{Default: &defaultText, Plan: &planText},
	}}
	params := collaborationModeParamsForTest("default", "catalog-model-a", "legacy instructions")

	item, err := router.collaborationModeWorldStateInputItem(string(threadID), params, info)
	if err != nil {
		t.Fatalf("initial collaboration state error = %v", err)
	}
	assertCollaborationModeItemText(t, item, "<collaboration_mode>"+defaultText+"</collaboration_mode>")
	if strings.Contains(collaborationModeItemText(item), "legacy") {
		t.Fatalf("catalog instructions did not override legacy: %#v", item)
	}

	unchanged, err := router.collaborationModeWorldStateInputItem(string(threadID), params, info)
	if err != nil || unchanged != nil {
		t.Fatalf("unchanged collaboration state item = %#v, err = %v", unchanged, err)
	}

	record, err := store.Load(threadID)
	if err != nil {
		t.Fatal(err)
	}
	persisted, ok := collaborationModeWorldStateSessionItemForTurn("turn-1", item, now)
	if !ok {
		t.Fatalf("collaboration item was not persistable: %#v", item)
	}
	record.Items = append(record.Items, persisted)
	record.Metadata.LastResponseID = "resp-1"
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	unchanged, err = router.collaborationModeWorldStateInputItem(string(threadID), params, info)
	if err != nil || unchanged != nil {
		t.Fatalf("retained collaboration state item = %#v, err = %v", unchanged, err)
	}

	params = collaborationModeParamsForTest("plan", "catalog-model-a", "legacy plan instructions")
	item, err = router.collaborationModeWorldStateInputItem(string(threadID), params, info)
	if err != nil {
		t.Fatal(err)
	}
	assertCollaborationModeItemText(t, item, "<collaboration_mode>"+planText+"</collaboration_mode>")

	modelBText := "model B collaboration instructions"
	modelB := &model.ModelInfo{Slug: "catalog-model-b", ModelMessages: &model.ModelMessages{
		CollaborationModes: &model.CollaborationModeMessages{Plan: &modelBText},
	}}
	params = collaborationModeParamsForTest("plan", "catalog-model-b", "legacy plan instructions")
	item, err = router.collaborationModeWorldStateInputItem(string(threadID), params, modelB)
	if err != nil {
		t.Fatal(err)
	}
	assertCollaborationModeItemText(t, item, "<collaboration_mode>"+modelBText+"</collaboration_mode>")

	stateRecord, err := store.Load(threadID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := session.DecodeWorldState(stateRecord.Metadata.WorldState)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot collaborationModeWorldStateSnapshot
	if err := json.Unmarshal(state.CollaborationMode, &snapshot); err != nil || snapshot.Mode != "plan" || snapshot.Model != "catalog-model-b" {
		t.Fatalf("persisted collaboration snapshot = %s, err = %v", state.CollaborationMode, err)
	}
}

func TestCollaborationModeWorldStatePreservesExplicitEmptyAndClearsMissing(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-collaboration-empty")
	now := time.Now().UTC()
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })

	empty := ""
	info := &model.ModelInfo{Slug: "catalog-model", ModelMessages: &model.ModelMessages{
		CollaborationModes: &model.CollaborationModeMessages{Default: &empty},
	}}
	item, err := router.collaborationModeWorldStateInputItem(string(threadID), collaborationModeParamsForTest("default", "catalog-model", "legacy"), info)
	if err != nil {
		t.Fatal(err)
	}
	assertCollaborationModeItemText(t, item, "<collaboration_mode></collaboration_mode>")

	record, err := store.Load(threadID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil || len(state.CollaborationMode) == 0 {
		t.Fatalf("explicit empty snapshot was not persisted: %s, err = %v", record.Metadata.WorldState, err)
	}

	missing := &model.ModelInfo{Slug: "catalog-model", ModelMessages: &model.ModelMessages{
		CollaborationModes: &model.CollaborationModeMessages{},
	}}
	item, err = router.collaborationModeWorldStateInputItem(string(threadID), collaborationModeParamsForTest("plan", "catalog-model", ""), missing)
	if err != nil {
		t.Fatal(err)
	}
	assertCollaborationModeItemText(t, item, "<collaboration_mode></collaboration_mode>")
	record, err = store.Load(threadID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil || len(state.CollaborationMode) == 0 {
		t.Fatalf("missing instructions must persist the empty-state snapshot once: %s, err = %v", record.Metadata.WorldState, err)
	}
	var cleared collaborationModeWorldStateSnapshot
	if err := json.Unmarshal(state.CollaborationMode, &cleared); err != nil || cleared.Mode != "plan" || cleared.Instructions == "" {
		t.Fatalf("persisted cleared snapshot = %s, err = %v", state.CollaborationMode, err)
	}
	item, err = router.collaborationModeWorldStateInputItem(string(threadID), collaborationModeParamsForTest("plan", "catalog-model", ""), missing)
	if err != nil || item != nil {
		t.Fatalf("absent-to-absent collaboration item = %#v, err = %v", item, err)
	}

	record.Metadata.WorldState, err = session.EncodeWorldState(&session.WorldState{CollaborationMode: json.RawMessage(`"default"`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	defaultText := "refreshed catalog default"
	refresh := &model.ModelInfo{Slug: "catalog-model", ModelMessages: &model.ModelMessages{
		CollaborationModes: &model.CollaborationModeMessages{Default: &defaultText},
	}}
	item, err = router.collaborationModeWorldStateInputItem(string(threadID), collaborationModeParamsForTest("default", "catalog-model", "stale legacy"), refresh)
	if err != nil {
		t.Fatal(err)
	}
	assertCollaborationModeItemText(t, item, "<collaboration_mode>"+defaultText+"</collaboration_mode>")
}

func collaborationModeParamsForTest(mode string, modelID string, legacy string) *turn.TurnStartParams {
	var developerInstructions any
	if legacy != "" {
		developerInstructions = legacy
	}
	return &turn.TurnStartParams{Model: modelID, CollaborationMode: map[string]any{
		"mode": mode,
		"settings": map[string]any{
			"model":                  modelID,
			"developer_instructions": developerInstructions,
		},
	}}
}

func assertCollaborationModeItemText(t *testing.T, item any, want string) {
	t.Helper()
	if got := collaborationModeItemText(item); got != want {
		t.Fatalf("collaboration item text = %q, want %q; item=%#v", got, want, item)
	}
}

func collaborationModeItemText(item any) string {
	raw, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(textFromInputItemContent(raw["content"]))
}
