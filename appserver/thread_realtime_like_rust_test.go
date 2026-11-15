package appserver

// Mirrors Rust's app-server thread_timeline realtime read tests (#61480):
// thread/realtime/list filters a fixed snapshot, finds an exact event, and a
// paginated fork exposes only the frames sealed in its own rollout.

import (
	"context"
	"testing"

	"codex_go/realtime"
)

func realtimeFrameItem(id, session, text string, observedAtMS int64) realtime.RealtimeItem {
	observed := observedAtMS
	return realtime.RealtimeItem{
		ID:                id,
		RealtimeSessionID: session,
		ObservedAtMS:      &observed,
		Content: realtime.RealtimeItemContent{
			Kind: realtime.RealtimeItemKindFrame,
			Role: realtime.RealtimeTranscriptRoleAssistant,
			Text: text,
		},
	}
}

func listThreadRealtimeLikeRust(t *testing.T, h *attachedThreadHarness, params ThreadRealtimeListParams) *ThreadRealtimeListResponse {
	t.Helper()
	response := h.router.Handle(requestWithParams(t, IntID(1), MethodThreadRealtimeList, params))
	if response.Error != nil {
		t.Fatalf("thread/realtime/list error = %+v", response.Error)
	}
	return response.Result.(*ThreadRealtimeListResponse)
}

func TestThreadRealtimeListFiltersAndFindsExactEventsLikeRust(t *testing.T) {
	h := newAttachedThreadHarness(t)
	record := h.createPaginatedThread(t, "0198f004-0000-7000-8000-000000000001", t.TempDir())
	threadID := string(record.ID)
	path, _ := record.Metadata.Extra["rollout_path"].(string)
	if path == "" {
		t.Fatal("paginated thread has no rollout path")
	}

	startedAt := int64(1000)
	started := realtime.RealtimeItem{
		ID:                "voice-1:started",
		RealtimeSessionID: "voice-1",
		ObservedAtMS:      &startedAt,
		Content:           realtime.RealtimeItemContent{Kind: realtime.RealtimeItemKindSessionStarted},
	}
	frame := realtimeFrameItem("voice-1:transcript-1", "voice-1", "Checking staging", 2100)
	if err := h.router.persistRealtimeHistoryItems(threadID, []realtime.RealtimeItem{started, frame}, true); err != nil {
		t.Fatalf("persist realtime items: %v", err)
	}
	if err := h.state.ReconcileRollout(context.Background(), path, false); err != nil {
		t.Fatalf("ReconcileRollout() error = %v", err)
	}

	session := "voice-1"
	from, to := int64(1000), int64(2000)
	window := listThreadRealtimeLikeRust(t, h, ThreadRealtimeListParams{
		ThreadID:         threadID,
		SessionID:        &session,
		FromObservedAtMS: &from,
		ToObservedAtMS:   &to,
		Limit:            intPointer(10),
	})
	if window.Coverage != ThreadRealtimeCoverageReady {
		t.Fatalf("window coverage = %q", window.Coverage)
	}
	if len(window.Data) != 1 {
		t.Fatalf("window data = %+v", window.Data)
	}
	if window.Data[0].Item.ID != "voice-1:started" || window.Data[0].Item.ObservedAtMS == nil || *window.Data[0].Item.ObservedAtMS != 1000 {
		t.Fatalf("window entry = %+v", window.Data[0])
	}

	eventID := "voice-1:transcript-1"
	byID := listThreadRealtimeLikeRust(t, h, ThreadRealtimeListParams{ThreadID: threadID, EventID: &eventID})
	if len(byID.Data) != 1 {
		t.Fatalf("event lookup data = %+v", byID.Data)
	}
	entry := byID.Data[0]
	if entry.Item.ObservedAtMS == nil || *entry.Item.ObservedAtMS != 2100 || entry.Item.Content.Kind != realtime.RealtimeItemKindFrame ||
		entry.Item.Content.Text != "Checking staging" {
		t.Fatalf("event lookup entry = %+v", entry)
	}

	missing := "missing-event"
	pending := listThreadRealtimeLikeRust(t, h, ThreadRealtimeListParams{ThreadID: threadID, EventID: &missing})
	if pending.Coverage != ThreadRealtimeCoveragePending || len(pending.Data) != 0 {
		t.Fatalf("missing event page = %+v", pending)
	}

	position := ThreadRealtimeOrderPosition
	first := listThreadRealtimeLikeRust(t, h, ThreadRealtimeListParams{
		ThreadID: threadID,
		Order:    &position,
		Limit:    intPointer(1),
	})
	if len(first.Data) != 1 || first.Data[0].Item.ID != "voice-1:started" || first.NextCursor == nil {
		t.Fatalf("first position page = %+v", first)
	}
	second := listThreadRealtimeLikeRust(t, h, ThreadRealtimeListParams{
		ThreadID: threadID,
		Order:    &position,
		Limit:    intPointer(1),
		Cursor:   first.NextCursor,
	})
	if len(second.Data) != 1 || second.Data[0].Item.ID != "voice-1:transcript-1" || second.NextCursor != nil {
		t.Fatalf("second position page = %+v", second)
	}
	if second.SnapshotEnd != first.SnapshotEnd {
		t.Fatalf("snapshot drifted from %d to %d", first.SnapshotEnd, second.SnapshotEnd)
	}
}

func TestThreadRealtimeListReadsOnlyChildFramesLikeRust(t *testing.T) {
	h := newAttachedThreadHarness(t)
	parent := h.createPaginatedThread(t, "0198f004-0000-7000-8000-000000000002", t.TempDir())
	parentID := string(parent.ID)
	parentPath, _ := parent.Metadata.Extra["rollout_path"].(string)
	if err := h.router.persistRealtimeHistoryItems(parentID, []realtime.RealtimeItem{
		realtimeFrameItem("parent-frame", "parent-call", "parent", 1000),
	}, true); err != nil {
		t.Fatalf("persist parent frame: %v", err)
	}
	if err := h.state.ReconcileRollout(context.Background(), parentPath, false); err != nil {
		t.Fatalf("ReconcileRollout(parent) error = %v", err)
	}

	fork := h.router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:       parentID,
		AttachToSource: true,
	}))
	if fork.Error != nil {
		t.Fatalf("thread/fork error = %+v", fork.Error)
	}
	childID := fork.Result.(*ThreadForkResponse).Thread.ID
	if err := h.router.persistRealtimeHistoryItems(childID, []realtime.RealtimeItem{
		realtimeFrameItem("child-frame", "child-call", "child", 1000),
	}, true); err != nil {
		t.Fatalf("persist child frame: %v", err)
	}

	for _, eventID := range []*string{nil, stringPointer("child-frame")} {
		page := listThreadRealtimeLikeRust(t, h, ThreadRealtimeListParams{ThreadID: childID, EventID: eventID})
		if page.Coverage != ThreadRealtimeCoverageReady {
			t.Fatalf("child coverage = %q", page.Coverage)
		}
		if len(page.Data) != 1 || page.Data[0].Item.ID != "child-frame" {
			t.Fatalf("child page = %+v, want only child-frame", page.Data)
		}
	}

	parentPage := listThreadRealtimeLikeRust(t, h, ThreadRealtimeListParams{ThreadID: parentID})
	if len(parentPage.Data) != 1 || parentPage.Data[0].Item.ID != "parent-frame" {
		t.Fatalf("parent page = %+v", parentPage.Data)
	}
}

