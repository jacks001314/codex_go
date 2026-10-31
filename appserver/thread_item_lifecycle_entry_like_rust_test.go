package appserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
)

// Mirrors Rust #47114: thread/items/list exposes the stored item lifecycle times
// as nullable startedAtMs/completedAtMs on each entry.
func TestThreadItemsListExposesStoredLifecycleTimestampsLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	router := NewRouter(session.NewStore(home))
	t.Cleanup(func() { _ = router.Close() })
	router.SetStateRuntime(runtime)

	now := fixedTime()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, SessionID: "lifecycle-history", ThreadID: "lifecycle-history", Source: "cli", CWD: home,
		ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []map[string]any{
		{
			"type": "item_completed", "thread_id": "lifecycle-history", "turn_id": "turn-1",
			"started_at_ms": int64(1000), "completed_at_ms": int64(2000),
			"item": map[string]any{"type": "agentMessage", "id": "agent-1", "text": "done", "phase": "final_answer"},
		},
		{
			"type": "item_completed", "thread_id": "lifecycle-history", "turn_id": "turn-1",
			"started_at_ms": int64(3000),
			"item":          map[string]any{"type": "agentMessage", "id": "agent-2", "text": "later", "phase": "final_answer"},
		},
	} {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(2 * time.Second).Format(time.RFC3339Nano), Payload: encoded}); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.AppendTurnComplete("turn-1", now.Add(5*time.Second), 5000); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadItemsList, ThreadItemsListParams{ThreadID: "lifecycle-history"}))
	if response.Error != nil {
		t.Fatalf("thread/items/list error = %+v", response.Error)
	}
	entries := map[string]ThreadItemEntry{}
	for _, entry := range response.Result.(*ThreadItemsListResponse).Data {
		entries[entry.Item.ID] = entry
	}
	assertEntryLifecycle := func(id string, wantStarted int64, wantCompleted *int64) {
		t.Helper()
		entry, ok := entries[id]
		if !ok {
			t.Fatalf("missing entry %s in %#v", id, entries)
		}
		if entry.StartedAtMS == nil || *entry.StartedAtMS != wantStarted {
			t.Fatalf("%s startedAtMs = %v, want %d", id, entry.StartedAtMS, wantStarted)
		}
		if wantCompleted == nil {
			if entry.CompletedAtMS != nil {
				t.Fatalf("%s completedAtMs = %d, want null", id, *entry.CompletedAtMS)
			}
			return
		}
		if entry.CompletedAtMS == nil || *entry.CompletedAtMS != *wantCompleted {
			t.Fatalf("%s completedAtMs = %v, want %d", id, entry.CompletedAtMS, *wantCompleted)
		}
	}
	completedAt := int64(2000)
	assertEntryLifecycle("agent-1", 1000, &completedAt)
	assertEntryLifecycle("agent-2", 3000, nil)

	encoded, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for _, entry := range payload.Data {
		if _, ok := entry["startedAtMs"]; !ok {
			t.Fatalf("entry omitted startedAtMs: %s", string(encoded))
		}
		if _, ok := entry["completedAtMs"]; !ok {
			t.Fatalf("entry omitted completedAtMs: %s", string(encoded))
		}
	}
}
