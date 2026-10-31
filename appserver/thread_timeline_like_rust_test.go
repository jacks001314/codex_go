package appserver

// Mirrors Rust's app-server thread_timeline test: the canonical timeline pages
// ordinary items, durable realtime items, and turn boundaries in rollout order,
// while thread/items/list keeps returning only ordinary items.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex_go/realtime"
	"codex_go/rollout"
)

func TestThreadTimelineListMixesItemsAndRealtimeLikeRust(t *testing.T) {
	h := newAttachedThreadHarness(t)
	record := h.createPaginatedThread(t, "timeline-thread", t.TempDir())
	threadID := string(record.ID)
	path, _ := record.Metadata.Extra["rollout_path"].(string)
	if path == "" {
		t.Fatal("paginated thread has no rollout path")
	}
	startedAt := time.Now().UTC()

	started := realtime.RealtimeItem{
		ID:                "voice-1:started",
		RealtimeSessionID: "voice-1",
		Content:           realtime.RealtimeItemContent{Kind: realtime.RealtimeItemKindSessionStarted},
	}
	h.router.persistRealtimeHistoryItems(threadID, []realtime.RealtimeItem{started})

	appender, err := rollout.Resume(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := appender.AppendTurnStartedWithRoot("", "turn-1", startedAt); err != nil {
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
	if err := appender.AppendLine(rollout.Line{Type: "event_msg", Timestamp: startedAt.Format(time.RFC3339Nano), Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if err := appender.Close(); err != nil {
		t.Fatal(err)
	}

	transcript := realtime.RealtimeItem{
		ID:                "voice-1:transcript-1",
		RealtimeSessionID: "voice-1",
		Content: realtime.RealtimeItemContent{
			Kind: realtime.RealtimeItemKindTranscriptSegment,
			Role: realtime.RealtimeTranscriptRoleAssistant,
			Text: "Checking staging",
		},
	}
	h.router.persistRealtimeHistoryItems(threadID, []realtime.RealtimeItem{transcript})

	completer, err := rollout.Resume(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := completer.AppendTurnCompleteWithRoot("", "turn-1", startedAt.Add(4*time.Second), 2000); err != nil {
		t.Fatal(err)
	}
	if err := completer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.state.ReconcileRollout(context.Background(), path, false); err != nil {
		t.Fatal(err)
	}

	response := h.router.Handle(requestWithParams(t, IntID(1), MethodThreadTimelineList, ThreadTimelineListParams{
		ThreadID: threadID,
		Limit:    intPointer(3),
	}))
	if response.Error != nil {
		t.Fatalf("thread/timeline/list error = %+v", response.Error)
	}
	page := response.Result.(*ThreadTimelineListResponse)
	if len(page.Data) != 3 {
		t.Fatalf("first page entries = %d, want 3", len(page.Data))
	}
	if entry := page.Data[0]; entry.Type != ThreadTimelineEntryItem || entry.TurnID != "turn-1" || entry.Item == nil || entry.Item.Type != "userMessage" || entry.Item.StartedAtMS == nil || *entry.Item.StartedAtMS != 0 || entry.Item.CompletedAtMS == nil || *entry.Item.CompletedAtMS != 1 {
		t.Fatalf("first entry = %+v", entry)
	}
	if entry := page.Data[1]; entry.Type != ThreadTimelineEntryRealtime || entry.Realtime == nil || entry.Realtime.Content.Text != "Checking staging" || entry.Realtime.Content.Kind != realtime.RealtimeItemKindTranscriptSegment {
		t.Fatalf("second entry = %+v", entry)
	}
	if entry := page.Data[2]; entry.Type != ThreadTimelineEntryTurnCompleted || entry.TurnID != "turn-1" || entry.Status != TurnStatusCompleted || entry.DurationMS == nil || *entry.DurationMS != 2000 {
		t.Fatalf("third entry = %+v", entry)
	}
	if page.ActiveRealtimeSessionAtPageStart == nil || *page.ActiveRealtimeSessionAtPageStart != "voice-1" {
		t.Fatalf("active realtime session at page start = %v", page.ActiveRealtimeSessionAtPageStart)
	}
	if page.NextCursor == nil {
		t.Fatal("first page is missing its next cursor")
	}

	older := h.router.Handle(requestWithParams(t, IntID(2), MethodThreadTimelineList, ThreadTimelineListParams{
		ThreadID: threadID,
		Cursor:   page.NextCursor,
		Limit:    intPointer(2),
	}))
	if older.Error != nil {
		t.Fatalf("older thread/timeline/list error = %+v", older.Error)
	}
	olderPage := older.Result.(*ThreadTimelineListResponse)
	if len(olderPage.Data) != 2 {
		t.Fatalf("older entries = %d, want 2", len(olderPage.Data))
	}
	if entry := olderPage.Data[0]; entry.Type != ThreadTimelineEntryRealtime || entry.Realtime == nil || entry.Realtime.ID != "voice-1:started" {
		t.Fatalf("older first entry = %+v", entry)
	}
	if entry := olderPage.Data[1]; entry.Type != ThreadTimelineEntryTurnStarted || entry.TurnID != "turn-1" {
		t.Fatalf("older second entry = %+v", entry)
	}
	if olderPage.ActiveRealtimeSessionAtPageStart != nil {
		t.Fatalf("older page active session = %v, want nil", *olderPage.ActiveRealtimeSessionAtPageStart)
	}
	if olderPage.NextCursor != nil {
		t.Fatalf("older page next cursor = %v, want nil", *olderPage.NextCursor)
	}

	ordinary := h.router.Handle(requestWithParams(t, IntID(3), MethodThreadItemsList, ThreadItemsListParams{ThreadID: threadID, Limit: intPointer(10)}))
	if ordinary.Error != nil {
		t.Fatalf("thread/items/list error = %+v", ordinary.Error)
	}
	if items := ordinary.Result.(*ThreadItemsListResponse).Data; len(items) != 1 || items[0].TurnID != "turn-1" {
		t.Fatalf("ordinary items = %+v, want only turn-1", items)
	}
}
