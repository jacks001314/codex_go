package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

// TestPaginatedForkResumeSkipsInheritedTokenUsageLikeRust mirrors Rust #57256:
// a paginated fork resume must not replay the parent's token usage as the
// child's activity, while an ordinary paginated resume still does.
func TestPaginatedForkResumeSkipsInheritedTokenUsageLikeRust(t *testing.T) {
	h := newAttachedThreadHarness(t)
	parent := h.createPaginatedThreadWithTurn(t, "0198f003-0000-7000-8000-000000000001", t.TempDir(), "0198f003-0000-7000-8000-0000000000t1", "parent message")
	fork := h.router.Handle(requestWithParams(t, IntID(1), MethodThreadFork, ThreadForkParams{
		ThreadID:       string(parent.ID),
		AttachToSource: true,
	}))
	if fork.Error != nil {
		t.Fatalf("attached thread/fork error = %+v", fork.Error)
	}
	childID := fork.Result.(*ThreadForkResponse).Thread.ID
	record, err := h.store.Read(session.ThreadID(childID), true, true)
	if err != nil {
		t.Fatalf("store.Read(%s) error = %v", childID, err)
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	record.Metadata.Extra["token_usage_turn_id"] = "0198f003-0000-7000-8000-0000000000t1"
	record.Metadata.Extra["token_usage_info"] = map[string]any{
		"total_token_usage": map[string]any{"input_tokens": float64(50), "output_tokens": float64(10), "total_tokens": float64(60)},
		"last_token_usage":  map[string]any{"input_tokens": float64(50), "output_tokens": float64(10), "total_tokens": float64(60)},
	}
	if err := h.store.Save(record); err != nil {
		t.Fatalf("store.Save(%s) error = %v", childID, err)
	}
	if record.ForkedFromID == "" {
		t.Fatalf("attached fork record lost its fork source")
	}
	if mode, found, err := h.state.ThreadHistoryMode(context.Background(), childID); err != nil || !found || !strings.EqualFold(strings.TrimSpace(mode), string(ThreadHistoryPaginated)) {
		t.Fatalf("attached fork history mode = %q found=%v err=%v", mode, found, err)
	}
	built := BuildThread(record, "", true)
	if built.ForkedFromID == nil || strings.TrimSpace(*built.ForkedFromID) == "" {
		t.Fatalf("built attached fork thread lost forkedFromId: %+v", built)
	}
	if mode, found, err := h.threadRouter.threadHistoryModeWithRepair(session.ThreadID(childID)); err != nil || !found || !strings.EqualFold(strings.TrimSpace(mode), string(ThreadHistoryPaginated)) {
		t.Fatalf("threadHistoryModeWithRepair = %q found=%v err=%v", mode, found, err)
	}

	before := len(h.sink.List())
	h.router.notifyRestoredTokenUsage(&ThreadResumeResponse{Thread: BuildThread(record, "", true)})
	if notifications := h.sink.List(); len(notifications) != before {
		t.Fatalf("paginated fork resume token usage notifications = %+v, want none", notifications[before:])
	}

	record.ForkedFromID = ""
	if err := h.store.Save(record); err != nil {
		t.Fatalf("store.Save(%s) error = %v", childID, err)
	}
	h.router.notifyRestoredTokenUsage(&ThreadResumeResponse{Thread: BuildThread(record, "", true)})
	if notifications := h.sink.List(); len(notifications) == before {
		t.Fatalf("ordinary paginated resume emitted no token usage notification")
	}
}

// createPaginatedThreadWithTurn persists a paginated thread whose own history
// holds one completed turn, so inherited and child-only views can be compared.
func (h *attachedThreadHarness) createPaginatedThreadWithTurn(t *testing.T, id string, cwd string, turnID string, text string) *session.Record {
	t.Helper()
	now := time.Now().UTC()
	path := rollout.PathForThread(h.home, id, now)
	record := &session.Record{
		ID:        session.ThreadID(id),
		SessionID: id,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           cwd,
			HistoryMode:   string(ThreadHistoryPaginated),
			ModelProvider: "openai",
			Source:        string(SessionSourceVsCode),
			Extra:         map[string]any{"rollout_path": path},
		},
	}
	if err := h.store.Create(record); err != nil {
		t.Fatalf("store.Create(%s) error = %v", id, err)
	}
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     h.home,
		ThreadID:      id,
		Source:        string(SessionSourceVsCode),
		CWD:           cwd,
		ModelProvider: "openai",
		HistoryMode:   string(ThreadHistoryPaginated),
		Now:           now,
	})
	if err != nil {
		t.Fatalf("rollout.NewRecorder(%s) error = %v", id, err)
	}
	if err := recorder.AppendTurnStarted(turnID, now); err != nil {
		t.Fatalf("AppendTurnStarted(%s) error = %v", id, err)
	}
	if err := recorder.AppendItem(rollout.Item{
		ID:   id + "-user",
		Type: "user_message",
		Text: text,
		Data: map[string]any{"turn_id": turnID},
	}); err != nil {
		t.Fatalf("AppendItem(%s) error = %v", id, err)
	}
	if err := recorder.AppendTurnComplete(turnID, now.Add(time.Second), 1000); err != nil {
		t.Fatalf("AppendTurnComplete(%s) error = %v", id, err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close(%s) error = %v", id, err)
	}
	if err := h.state.ReconcileRollout(context.Background(), path, false); err != nil {
		t.Fatalf("ReconcileRollout(%s) error = %v", id, err)
	}
	return record
}

func listThreadItemsLikeRust(t *testing.T, h *attachedThreadHarness, threadID string) []ThreadItemEntry {
	t.Helper()
	response := h.router.Handle(requestWithParams(t, IntID(1), MethodThreadItemsList, ThreadItemsListParams{ThreadID: threadID}))
	if response.Error != nil {
		t.Fatalf("thread/items/list(%s) error = %+v", threadID, response.Error)
	}
	return response.Result.(*ThreadItemsListResponse).Data
}

// TestAttachedThreadViewsStayChildOnlyLikeRust mirrors Rust #57256: an attached
// fork keeps inherited context for model requests but exposes only its own turns
// through items, turns, reads, search, and turn-specific fork boundaries.
func TestAttachedThreadViewsStayChildOnlyLikeRust(t *testing.T) {
	h := newAttachedThreadHarness(t)
	const (
		parentID     = "0198f002-0000-7000-8000-000000000001"
		parentTurnID = "0198f002-0000-7000-8000-0000000000t1"
	)
	parent := h.createPaginatedThreadWithTurn(t, parentID, t.TempDir(), parentTurnID, "parent-only message")

	if items := listThreadItemsLikeRust(t, h, string(parent.ID)); len(items) == 0 {
		t.Fatalf("parent items list is empty; fixture did not materialize")
	}
	fork := h.router.Handle(requestWithParams(t, IntID(2), MethodThreadFork, ThreadForkParams{
		ThreadID:       string(parent.ID),
		AttachToSource: true,
	}))
	if fork.Error != nil {
		t.Fatalf("attached thread/fork error = %+v", fork.Error)
	}
	childID := fork.Result.(*ThreadForkResponse).Thread.ID

	if items := listThreadItemsLikeRust(t, h, childID); len(items) != 0 {
		t.Fatalf("attached items list = %+v, want no inherited items", items)
	}
	turns := h.router.Handle(requestWithParams(t, IntID(3), MethodThreadTurnsList, ThreadTurnsListParams{ThreadID: childID}))
	if turns.Error != nil {
		t.Fatalf("thread/turns/list error = %+v", turns.Error)
	}
	if page := turns.Result.(*TurnsPage); page != nil && len(page.Data) != 0 {
		t.Fatalf("attached turns list = %+v, want no inherited turns", page.Data)
	}
	read := h.router.Handle(requestWithParams(t, IntID(4), MethodThreadRead, ThreadReadParams{ThreadID: childID, IncludeTurns: true}))
	if read.Error != nil {
		t.Fatalf("thread/read error = %+v", read.Error)
	}
	if thread := read.Result.(*ThreadReadResponse).Thread; len(thread.Turns) != 0 {
		t.Fatalf("attached read turns = %+v, want no inherited turns", thread.Turns)
	}
	// thread/searchOccurrences is an experimental method that the runtime
	// router gates on initialize capabilities; exercise the thread router
	// directly, as the paginated-history tests do.
	search := h.threadRouter.Handle(requestWithParams(t, IntID(5), MethodThreadSearchOccurrences, ThreadSearchOccurrencesParams{
		ThreadID:   childID,
		SearchTerm: "parent-only",
	}))
	if search.Error != nil {
		t.Fatalf("thread/searchOccurrences error = %+v", search.Error)
	}
	if occurrences := search.Result.(*ThreadSearchOccurrencesResponse); len(occurrences.Data) != 0 {
		t.Fatalf("attached search occurrences = %+v, want no inherited matches", occurrences.Data)
	}
	for _, params := range []ThreadForkParams{
		{ThreadID: childID, LastTurnID: parentTurnID},
		{ThreadID: childID, BeforeTurnID: parentTurnID},
	} {
		rejected := h.router.Handle(requestWithParams(t, IntID(6), MethodThreadFork, params))
		if rejected.Error == nil || !strings.Contains(rejected.Error.Message, "turn not found") {
			t.Fatalf("fork through inherited turn error = %+v, want turn-not-found", rejected.Error)
		}
	}
	revert := h.threadRouter.Handle(requestWithParams(t, IntID(7), MethodThreadRevert, ThreadRevertParams{
		ThreadID:     childID,
		BeforeTurnID: parentTurnID,
	}))
	if revert.Error == nil {
		t.Fatalf("revert through inherited turn succeeded, want a rejection")
	}
	if _, err := h.store.Read(session.ThreadID(childID), true, false); err != nil {
		t.Fatalf("rejected revert removed attached thread: %v", err)
	}
}
