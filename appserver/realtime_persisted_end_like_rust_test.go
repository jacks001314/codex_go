package appserver

// Mirrors Rust #62299: an open call whose transport is gone can be ended from
// persisted history after a reload, and a call that already recorded its
// closing marker is left alone.

import (
	"codex_go/rollout"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/state"
)

func openPersistedRealtimeThread(t *testing.T) (*RuntimeRouter, *state.StateRuntime, string, *session.Store) {
	t.Helper()
	ctx := context.Background()
	home := t.TempDir()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, sqliteConfig, "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	store := session.NewStore(filepath.Join(home, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		StateRuntime: runtime,
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if start.Error != nil {
		t.Fatalf("thread/start: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	return router, runtime, threadID, store
}

func insertPersistedRealtimeItem(t *testing.T, runtime *state.StateRuntime, threadID string, position int64, item map[string]any) {
	t.Helper()
	ctx := context.Background()
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	itemType, _ := item["type"].(string)
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO thread_realtime_items (thread_id, item_id, rollout_ordinal, created_at_ms, item_type, item_json)
VALUES (?, ?, ?, 0, ?, ?)`, threadID, item["id"], position, itemType, string(encoded)); err != nil {
		t.Fatal(err)
	}
}

func TestEndPersistedRealtimeSessionLikeRust(t *testing.T) {
	router, runtime, threadID, _ := openPersistedRealtimeThread(t)
	insertPersistedRealtimeItem(t, runtime, threadID, 0, map[string]any{"id": "voice-1:started", "realtime_session_id": "voice-1", "observed_at_ms": 100, "type": "realtime_session_started"})

	if !router.endPersistedRealtimeSession(threadID) {
		t.Fatal("an open persisted call was not ended")
	}
	if closed := closedMarkersInRollout(t, router, threadID, "voice-1"); closed != 1 {
		t.Fatalf("closed markers = %d, want exactly 1", closed)
	}
	// A second stop must not append another closing marker.
	if router.endPersistedRealtimeSession(threadID) {
		t.Fatal("an already closed call was ended again")
	}
}

func TestRealtimeStopEndsPersistedSessionLikeRust(t *testing.T) {
	router, runtime, threadID, _ := openPersistedRealtimeThread(t)
	insertPersistedRealtimeItem(t, runtime, threadID, 0, map[string]any{"id": "voice-1:started", "realtime_session_id": "voice-1", "observed_at_ms": 100, "type": "realtime_session_started"})

	if result := router.Handle(requestWithParams(t, IntID(2), MethodThreadRealtimeStop, map[string]any{"threadId": threadID})); result.Error != nil {
		t.Fatalf("thread/realtime/stop: %+v", result.Error)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if closedMarkersInRollout(t, router, threadID, "voice-1") == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("stop did not end the persisted call")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func closedMarkersInRollout(t *testing.T, router *RuntimeRouter, threadID string, sessionID string) int {
	t.Helper()
	path, err := rollout.FindThreadPath(router.codexHomeForRollout(), threadID, false)
	if err != nil || path == "" {
		return 0
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		return 0
	}
	closed := 0
	for index := range lines {
		line := &lines[index]
		if line.Type != "realtime_item" {
			continue
		}
		payload := line.RealtimeItem
		if len(payload) == 0 {
			payload = line.Payload
		}
		var item struct {
			Type              string `json:"type"`
			RealtimeSessionID string `json:"realtime_session_id"`
		}
		if json.Unmarshal(payload, &item) == nil && item.Type == "realtime_session_closed" && item.RealtimeSessionID == sessionID {
			closed++
		}
	}
	return closed
}
