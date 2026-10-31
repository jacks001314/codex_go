package state

// Mirrors Rust's thread_timeline.rs: pages mix ordinary items, realtime items,
// and turn boundaries in rollout order, and resolve the realtime session that
// was open before each page.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"codex_go/rollout"
)

func TestListThreadTimelineMixesItemsAndResolvesOpeningSessionLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	threadID := "timeline-thread"
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, SessionID: threadID, ThreadID: threadID, CWD: filepath.Join(home, "repo"),
		ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rollout.AppendRealtimeItems(recorder, []json.RawMessage{
		json.RawMessage(`{"id":"voice-1:started","realtime_session_id":"voice-1","type":"realtime_session_started"}`),
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStartedWithRoot("", "turn-1", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"type": "item_completed", "thread_id": threadID, "turn_id": "turn-1",
		"started_at_ms": 0, "completed_at_ms": 1,
		"item": map[string]any{
			"type":    "userMessage",
			"id":      "user-1",
			"content": []map[string]any{{"type": "text", "text": "check staging"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(2 * time.Second).Format(time.RFC3339Nano), Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if err := rollout.AppendRealtimeItems(recorder, []json.RawMessage{
		json.RawMessage(`{"id":"voice-1:transcript-1","realtime_session_id":"voice-1","type":"transcript_segment","role":"assistant","text":"Checking staging"}`),
	}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnCompleteWithRoot("", "turn-1", now.Add(4*time.Second), 2000); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	page, err := runtime.ListThreadTimeline(ctx, ThreadTimelineListParams{ThreadID: threadID, PageSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 3 {
		t.Fatalf("first page entries = %d, want 3", len(page.Entries))
	}
	if got := page.Entries[0]; got.Kind != ThreadTimelineItem || got.TurnID != "turn-1" || got.ID != "user-1" || got.StartedAtMS == nil || *got.StartedAtMS != 0 || got.CompletedAtMS == nil || *got.CompletedAtMS != 1 {
		t.Fatalf("first entry = %+v", got)
	}
	var transcript struct {
		Text string `json:"text"`
	}
	if got := page.Entries[1]; got.Kind != ThreadTimelineRealtimeItem || json.Unmarshal(got.RealtimeItemJSON, &transcript) != nil || transcript.Text != "Checking staging" {
		t.Fatalf("second entry = %+v (%s)", got, got.RealtimeItemJSON)
	}
	if got := page.Entries[2]; got.Kind != ThreadTimelineTurnCompleted || got.TurnID != "turn-1" || got.Status != "completed" || got.DurationMS == nil || *got.DurationMS != 2000 {
		t.Fatalf("third entry = %+v", got)
	}
	if page.ActiveRealtimeSessionAtPageStart == nil || *page.ActiveRealtimeSessionAtPageStart != "voice-1" {
		t.Fatalf("active session at first page start = %v", page.ActiveRealtimeSessionAtPageStart)
	}
	if page.NextCursor == nil {
		t.Fatal("first page has no next cursor")
	}

	older, err := runtime.ListThreadTimeline(ctx, ThreadTimelineListParams{ThreadID: threadID, Cursor: page.NextCursor, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Entries) != 2 {
		t.Fatalf("older entries = %d, want 2", len(older.Entries))
	}
	if got := older.Entries[0]; got.Kind != ThreadTimelineRealtimeItem || got.ID != "voice-1:started" {
		t.Fatalf("older first entry = %+v", got)
	}
	if got := older.Entries[1]; got.Kind != ThreadTimelineTurnStarted || got.TurnID != "turn-1" || got.StartedAt == nil || *got.StartedAt != now.Add(time.Second).Unix() {
		t.Fatalf("older second entry = %+v", got)
	}
	if older.ActiveRealtimeSessionAtPageStart != nil {
		t.Fatalf("older active session = %v, want nil", *older.ActiveRealtimeSessionAtPageStart)
	}
	if older.NextCursor != nil {
		t.Fatalf("older next cursor = %v, want nil", *older.NextCursor)
	}

	wrongThread := `{"threadId":"other","position":1,"kind":1,"id":"x"}`
	if _, err := runtime.ListThreadTimeline(ctx, ThreadTimelineListParams{ThreadID: threadID, Cursor: &wrongThread, PageSize: 1}); !isInvalidHistoryError(err) {
		t.Fatalf("wrong-thread cursor error = %v", err)
	}
}
