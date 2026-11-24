package appserver

// Mirrors Rust #62271: a thread that already persisted a canonical realtime
// session start stays enrolled after `features.realtime_frames_local` is turned
// off, so a call in flight keeps recording frames.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"codex_go/session"
	"codex_go/state"
)

func TestRealtimeFramesStayEnrolledAfterFlagDisabledLikeRust(t *testing.T) {
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
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		StateRuntime: runtime,
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if start.Error != nil {
		t.Fatalf("thread/start: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	if router.realtimeFramesEnabled(threadID) {
		t.Fatal("a thread without a persisted session start enrolled with the flag off")
	}

	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	item, err := json.Marshal(map[string]any{
		"id":                  "voice-1:started",
		"realtime_session_id": "voice-1",
		"observed_at_ms":      100,
		"type":                "realtime_session_started",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO thread_realtime_items (thread_id, item_id, rollout_ordinal, created_at_ms, item_type, item_json)
VALUES (?, ?, 0, 0, 'realtime_session_started', ?)`, threadID, "voice-1:started", string(item)); err != nil {
		t.Fatal(err)
	}
	if !router.realtimeFramesEnabled(threadID) {
		t.Fatal("a persisted session start did not keep the thread enrolled")
	}
	if router.realtimeFramesEnabled("another-thread") {
		t.Fatal("enrollment leaked to a thread without a session start")
	}
	if head, found, err := runtime.LatestThreadRealtimeSession(ctx, threadID); err != nil || !found || head != "voice-1" {
		t.Fatalf("latest session = %q/%v, %v", head, found, err)
	}
	session, err := runtime.LoadThreadRealtimeSession(ctx, threadID, "voice-1")
	if err != nil || session == nil || len(session.Items) != 1 {
		t.Fatalf("load session = %#v, %v", session, err)
	}
}
