package state

// Mirrors Rust #62271's thread-store session recovery: a logical call is
// loaded only when durable history holds its start record, the newest start is
// the fallback head (closed sessions included), and a persisted start keeps a
// thread enrolled.

import (
	"context"
	"encoding/json"
	"testing"
)

func insertRealtimeSessionRow(t *testing.T, runtime *StateRuntime, threadID string, position int64, itemType string, item map[string]any) {
	t.Helper()
	ctx := context.Background()
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	item["type"] = itemType
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

func TestLoadThreadRealtimeSessionLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	threadID := "realtime-session-thread"
	insertRealtimeSessionRow(t, runtime, threadID, 0, "realtime_session_started", map[string]any{"id": "voice-1:started", "realtime_session_id": "voice-1", "observed_at_ms": 100})
	insertRealtimeSessionRow(t, runtime, threadID, 1, "transcript_segment", map[string]any{"id": "voice-1:transcript", "realtime_session_id": "voice-1", "observed_at_ms": 200, "role": "user", "text": "hi"})
	insertRealtimeSessionRow(t, runtime, threadID, 2, "realtime_session_closed", map[string]any{"id": "voice-1:closed", "realtime_session_id": "voice-1", "observed_at_ms": 300, "outcome": "ended"})
	insertRealtimeSessionRow(t, runtime, threadID, 3, "realtime_session_started", map[string]any{"id": "voice-2:started", "realtime_session_id": "voice-2", "observed_at_ms": 400})
	// A session with frames but no start record is not a recoverable call.
	insertRealtimeSessionRow(t, runtime, threadID, 4, "transcript_segment", map[string]any{"id": "voice-3:transcript", "realtime_session_id": "voice-3", "observed_at_ms": 500, "role": "user", "text": "orphan"})

	session, err := runtime.LoadThreadRealtimeSession(ctx, threadID, "voice-1")
	if err != nil {
		t.Fatal(err)
	}
	if session == nil || session.SessionID != "voice-1" || len(session.Items) != 3 {
		t.Fatalf("session = %#v", session)
	}
	if session.Items[0].Position != 0 || session.Items[2].Position != 2 {
		t.Fatalf("session positions = %v, %v", session.Items[0].Position, session.Items[2].Position)
	}
	if session.Items[1].ObservedAtMS == nil || *session.Items[1].ObservedAtMS != 200 {
		t.Fatalf("session item observation = %#v", session.Items[1])
	}

	missing, err := runtime.LoadThreadRealtimeSession(ctx, threadID, "voice-3")
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatalf("session without a start record = %#v", missing)
	}
	unknown, err := runtime.LoadThreadRealtimeSession(ctx, threadID, "voice-9")
	if err != nil || unknown != nil {
		t.Fatalf("unknown session = %#v, %v", unknown, err)
	}
}

func TestLatestThreadRealtimeSessionLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	threadID := "realtime-head-thread"
	insertRealtimeSessionRow(t, runtime, threadID, 0, "realtime_session_started", map[string]any{"id": "voice-1:started", "realtime_session_id": "voice-1", "observed_at_ms": 100})
	insertRealtimeSessionRow(t, runtime, threadID, 1, "realtime_session_closed", map[string]any{"id": "voice-1:closed", "realtime_session_id": "voice-1", "observed_at_ms": 200, "outcome": "ended"})
	insertRealtimeSessionRow(t, runtime, threadID, 2, "realtime_session_started", map[string]any{"id": "voice-2:started", "realtime_session_id": "voice-2", "observed_at_ms": 300})

	head, found, err := runtime.LatestThreadRealtimeSession(ctx, threadID)
	if err != nil || !found || head != "voice-2" {
		t.Fatalf("head = %q/%v, %v; want voice-2", head, found, err)
	}
	// A closed session still names the head until a newer one starts.
	if _, found, err := runtime.LatestThreadRealtimeSession(ctx, "empty-thread"); err != nil || found {
		t.Fatalf("empty head found=%v err=%v", found, err)
	}
}

func TestThreadRealtimeEnrolledLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	threadID := "realtime-enrolled-thread"
	if enrolled, err := runtime.ThreadRealtimeEnrolled(ctx, threadID); err != nil || enrolled {
		t.Fatalf("empty thread enrolled=%v err=%v", enrolled, err)
	}
	insertRealtimeSessionRow(t, runtime, threadID, 0, "transcript_segment", map[string]any{"id": "frame", "realtime_session_id": "voice-1", "observed_at_ms": 100, "role": "user", "text": "hi"})
	if enrolled, err := runtime.ThreadRealtimeEnrolled(ctx, threadID); err != nil || enrolled {
		t.Fatalf("frames without a start enrolled=%v err=%v", enrolled, err)
	}
	insertRealtimeSessionRow(t, runtime, threadID, 1, "realtime_session_started", map[string]any{"id": "voice-1:started", "realtime_session_id": "voice-1", "observed_at_ms": 200})
	if enrolled, err := runtime.ThreadRealtimeEnrolled(ctx, threadID); err != nil || !enrolled {
		t.Fatalf("persisted start enrolled=%v err=%v", enrolled, err)
	}
}
