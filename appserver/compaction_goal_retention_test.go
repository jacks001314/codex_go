package appserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/compact"
	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/session"
)

// Rust #49598 (`de02016798`) `Session::replace_compacted_history`
// (`codex-rs/core/src/session/mod.rs`): "Goal edits are published outside the
// running task. Keep edits accepted after the compaction input snapshot, in
// their original order, after its replacement." Rust's own test for the
// boundary lives in `core/src/session/tests.rs` (goal edits survive
// compaction); Go drives the same window through a runner that publishes the
// edit mid-compaction.

// goalEditsDuringCompactRunner publishes goal edits while the compaction is
// running, inside the window Rust guards with `input_goal_ids`.
type goalEditsDuringCompactRunner struct {
	router *RuntimeRouter
	// publishGoalEdit uses the real goal handler path (a `thread/goal/clear`
	// instruction) instead of appending an item directly.
	publishGoalEdit bool
	// appendItem is appended to the live thread through the same store route the
	// goal handler uses.
	appendItem *session.Item
	// echoAppendedIntoReplacement copies appendItem into the replacement, which
	// is how a collected user message can already cover a published edit.
	echoAppendedIntoReplacement bool
	replacement                 []compact.Item
}

func (r *goalEditsDuringCompactRunner) Compact(ctx context.Context, request *compact.Request) (*compact.Result, error) {
	if r.publishGoalEdit {
		if err := r.router.recordUserGoalUpdate(request.ThreadID, "", contextfrag.NewUserGoalClear()); err != nil {
			return nil, err
		}
	}
	if r.appendItem != nil {
		if _, err := r.router.services.ThreadRouter.appendThreadItems(session.ThreadID(request.ThreadID), []session.Item{*r.appendItem}); err != nil {
			return nil, err
		}
	}
	replacement := append([]compact.Item(nil), r.replacement...)
	if r.echoAppendedIntoReplacement && r.appendItem != nil {
		// A replacement that already carries the edit. It is modelled as a
		// retained user message because that is the shape the compaction
		// replacement actually keeps (`compact.ShouldKeepCompactedHistoryItem`);
		// its host annotation still identifies it as a goal instruction.
		replacement = append(replacement, compact.Item{
			ID:   r.appendItem.ID,
			Type: r.appendItem.Type,
			Role: r.appendItem.Role,
			Kind: "user_message",
			Text: "User cleared the goal.",
			Raw:  r.appendItem.Raw,
		})
	}
	if len(replacement) == 0 {
		replacement = compact.BuildCompactedHistory(nil, nil, "compaction summary")
	}
	return &compact.Result{
		Status:      compact.StatusCompleted,
		Request:     *request,
		Summary:     "compaction summary",
		NewHistory:  replacement,
		CompletedAt: time.Now().UTC(),
		Source:      compact.SourceRemote,
	}, nil
}

func newGoalRetentionContext(t *testing.T, items []session.Item, runner compact.RemoteRunner) (*RuntimeRouter, *session.Store, string) {
	t.Helper()
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	const threadID = "thread-goal-retention"
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: threadID,
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Model: "gpt-5.4", CWD: home, Extra: map[string]any{}},
		Items:    items,
	}); err != nil {
		t.Fatalf("Create record error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:  NewRouter(store),
		Config:        config.NewConfigService(home),
		CompactRunner: runner,
	})
	return router, store, threadID
}

func compactGoalRetentionThread(t *testing.T, router *RuntimeRouter, threadID string) []session.Item {
	t.Helper()
	if _, err := router.compactThread(context.Background(), &runtimeCompactRequest{
		ThreadID: threadID,
		TurnID:   "turn-compact-goal",
		Trigger:  compact.TriggerAuto,
		Reason:   compact.ReasonTokenLimit,
		Phase:    compact.PhaseMidTurn,
	}); err != nil {
		t.Fatalf("compactThread() error = %v", err)
	}
	record, err := router.services.ThreadRouter.store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read record error = %v", err)
	}
	return record.Items
}

func goalItemText(t *testing.T, item session.Item) string {
	t.Helper()
	text, ok := userGoalInstructionText(item.Raw)
	if !ok {
		t.Fatalf("item %q is not a host-annotated goal instruction: %s", item.ID, item.Raw)
	}
	return text
}

func countGoalItemsByID(items []session.Item, id string) int {
	count := 0
	for i := range items {
		if strings.TrimSpace(items[i].ID) == id {
			count++
		}
	}
	return count
}

// An edit published while the compaction ran is neither summarized nor part of
// the replacement, so it is re-appended in its original order after the
// replacement (Rust `replace_compacted_history`, #49598).
func TestCompactionRetainsGoalEditsPublishedDuringCompactionLikeRust(t *testing.T) {
	now := time.Now().UTC()
	runner := &goalEditsDuringCompactRunner{publishGoalEdit: true}
	router, _, threadID := newGoalRetentionContext(t, []session.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: now},
	}, runner)
	runner.router = router

	items := compactGoalRetentionThread(t, router, threadID)

	var goalIndex = -1
	for i := range items {
		if _, ok := userGoalInstructionText(items[i].Raw); ok {
			if goalIndex != -1 {
				t.Fatalf("published goal edit appears twice: %+v", items)
			}
			goalIndex = i
		}
	}
	if goalIndex == -1 {
		t.Fatalf("the goal edit published during compaction was dropped: %+v", items)
	}
	if text := goalItemText(t, items[goalIndex]); !strings.Contains(text, "User cleared the goal.") {
		t.Fatalf("retained goal edit text = %q", text)
	}
	// The retained edit keeps its original order: after the replacement and
	// before the compaction lifecycle item that closes the record.
	if goalIndex != len(items)-2 {
		t.Fatalf("retained goal edit at %d, want %d (one before the compaction item): %+v", goalIndex, len(items)-2, items)
	}
	if last := items[len(items)-1].Type; last != "contextCompaction" {
		t.Fatalf("last item = %q, want the context compaction item", last)
	}
}

// A goal edit already inside the compaction input is covered by
// `input_goal_ids`, so the carry-over does not resurrect it.
//
// Rust keeps the two halves consistent: `parse_user_message`
// (`codex-rs/core/src/event_mapping.rs`) rejects a contextual fragment, and the
// goal wrapper matches `InternalModelContextFragment` (`source="user_goal"` is a
// valid `[a-z][a-z0-9_]*` source), so `collect_annotated_user_messages` never
// carries an input goal edit into the replacement. Go's
// `compact.ShouldKeepCompactedHistoryItem` drops the same item through its kind
// allowlist. Without the `input_goal_ids` gate the live copy would be appended,
// duplicating an edit the compaction already summarized.
func TestCompactionDoesNotCarryInputGoalEditLikeRust(t *testing.T) {
	now := time.Now().UTC()
	goal, ok := userGoalInstructionItem(contextfrag.NewUserGoalClear())
	if !ok {
		t.Fatal("goal instruction did not render")
	}
	goal.CreatedAt = now
	runner := &goalEditsDuringCompactRunner{}
	router, _, threadID := newGoalRetentionContext(t, []session.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: now},
		goal,
	}, runner)
	runner.router = router

	items := compactGoalRetentionThread(t, router, threadID)

	if count := countGoalItemsByID(items, goal.ID); count != 0 {
		t.Fatalf("input goal edit count = %d, want 0 (input ids must gate the carry-over): %+v", count, items)
	}
}

// A published edit the replacement already carries is not appended again
// (Rust's `replacement_goal_ids`, `Session::replace_compacted_history`).
func TestCompactionDoesNotDuplicateReplacementGoalEditLikeRust(t *testing.T) {
	now := time.Now().UTC()
	goal, ok := userGoalInstructionItem(contextfrag.NewUserGoalSet(stringPtrIfNotEmpty("ship it"), nil))
	if !ok {
		t.Fatal("goal instruction did not render")
	}
	goal.CreatedAt = now
	runner := &goalEditsDuringCompactRunner{
		appendItem:                  &goal,
		echoAppendedIntoReplacement: true,
	}
	router, _, threadID := newGoalRetentionContext(t, []session.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "first", CreatedAt: now},
	}, runner)
	runner.router = router

	items := compactGoalRetentionThread(t, router, threadID)

	if count := countGoalItemsByID(items, goal.ID); count != 1 {
		t.Fatalf("replacement goal edit count = %d, want 1: %+v", count, items)
	}
}

// userGoalInstructionText must reject a matching text wrapper that is not
// host-annotated, mirroring Rust `UserGoalUpdate::message_text`.
func TestGoalInstructionTextRequiresHostAnnotationLikeRust(t *testing.T) {
	if _, ok := userGoalInstructionText([]byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":"<codex_internal_context source=\"user_goal\">\nUser cleared the goal.\n</codex_internal_context>"}]}`)); ok {
		t.Fatal("an unannotated wrapper must not count as a goal instruction")
	}
	if _, ok := userGoalInstructionText([]byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":"x"},{"type":"input_text","text":"y"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.goal"]}}`)); ok {
		t.Fatal("a multi-content message must not count as a goal instruction")
	}
	if _, ok := userGoalInstructionText([]byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":"x"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.goal","user.text"]}}`)); ok {
		t.Fatal("a message with several content kinds must not count as a goal instruction")
	}
	if _, ok := userGoalInstructionText([]byte(`{"type":"message","role":"user","content":[{"type":"input_text","text":"x"}],"internal_chat_message_metadata_passthrough":{"content_item_kinds":["user.goal.omitted"]}}`)); !ok {
		t.Fatal("an omitted-objective goal instruction must count")
	}
}
