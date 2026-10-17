package state

import (
	"context"
	"testing"
	"time"
)

// TestThreadReadStateRevisionChecksAndDeduplicationLikeRust mirrors
// state/src/runtime/thread_read_state_tests.rs: reads do not materialize a
// receipt, stale edits return the current state, and publishing the same
// terminal turn twice changes nothing.
func TestThreadReadStateRevisionChecksAndDeduplicationLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime := newBackfillTestRuntimeAt(t, home)
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	for _, threadID := range []string{"read-state-thread", "other-read-state-thread"} {
		path := writeBackfillRollout(t, home, threadID, now, false)
		if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
			t.Fatal(err)
		}
	}

	states, err := runtime.ThreadReadStates(ctx, []string{"read-state-thread", "other-read-state-thread"})
	if err != nil {
		t.Fatal(err)
	}
	baseline := states["read-state-thread"]
	if baseline.FirstUnreadTurn != nil || baseline.Revision != "initial:read-state-thread" {
		t.Fatalf("baseline = %#v", baseline)
	}
	var receiptCount int
	if err := runtime.StateDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM thread_read_receipts`).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 0 {
		t.Fatalf("reading snapshots created %d receipt rows", receiptCount)
	}

	conflict, err := runtime.UpdateThreadReadState(ctx, "other-read-state-thread", baseline.Revision, ReadStateOperationUnread)
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Kind != ReadStateUpdateConflict || conflict.State.Revision != "initial:other-read-state-thread" {
		t.Fatalf("cross-thread edit = %#v", conflict)
	}

	marked, err := runtime.UpdateThreadReadState(ctx, "read-state-thread", baseline.Revision, ReadStateOperationUnread)
	if err != nil {
		t.Fatal(err)
	}
	if marked.Kind != ReadStateUpdateApplied || marked.State.FirstUnreadTurn == nil || *marked.State.FirstUnreadTurn != "" {
		t.Fatalf("mark unread = %#v", marked)
	}
	stale, err := runtime.UpdateThreadReadState(ctx, "read-state-thread", baseline.Revision, ReadStateOperationRead)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Kind != ReadStateUpdateConflict || !sameThreadReadState(stale.State, marked.State) {
		t.Fatalf("stale read = %#v, want conflict with %#v", stale, marked.State)
	}

	markedAgain, err := runtime.UpdateThreadReadState(ctx, "read-state-thread", marked.State.Revision, ReadStateOperationUnread)
	if err != nil {
		t.Fatal(err)
	}
	if markedAgain.Kind != ReadStateUpdateApplied || markedAgain.State.Revision == marked.State.Revision {
		t.Fatalf("second mark = %#v", markedAgain)
	}
	stale, err = runtime.UpdateThreadReadState(ctx, "read-state-thread", marked.State.Revision, ReadStateOperationRead)
	if err != nil {
		t.Fatal(err)
	}
	if stale.Kind != ReadStateUpdateConflict || !sameThreadReadState(stale.State, markedAgain.State) {
		t.Fatalf("stale read after repeated mark = %#v", stale)
	}

	read, err := runtime.UpdateThreadReadState(ctx, "read-state-thread", markedAgain.State.Revision, ReadStateOperationRead)
	if err != nil {
		t.Fatal(err)
	}
	if read.Kind != ReadStateUpdateApplied || read.State.FirstUnreadTurn != nil {
		t.Fatalf("mark read = %#v", read)
	}
	if changed, err := runtime.PublishThreadAttention(ctx, "read-state-thread", "turn-1"); err != nil || !changed {
		t.Fatalf("publish turn-1 = %v, %v", changed, err)
	}
	first := mustThreadReadState(t, ctx, runtime, "read-state-thread")
	if first.FirstUnreadTurn == nil || *first.FirstUnreadTurn != "turn-1" {
		t.Fatalf("first published state = %#v", first)
	}
	if changed, err := runtime.PublishThreadAttention(ctx, "read-state-thread", "turn-1"); err != nil || changed {
		t.Fatalf("duplicate publish = %v, %v", changed, err)
	}
	if got := mustThreadReadState(t, ctx, runtime, "read-state-thread"); !sameThreadReadState(got, first) {
		t.Fatalf("duplicate publication changed state: %#v -> %#v", first, got)
	}
	if changed, err := runtime.PublishThreadAttention(ctx, "read-state-thread", "turn-2"); err != nil || !changed {
		t.Fatalf("publish turn-2 = %v, %v", changed, err)
	}
	second := mustThreadReadState(t, ctx, runtime, "read-state-thread")
	if second.FirstUnreadTurn == nil || *second.FirstUnreadTurn != "turn-1" || second.Revision == first.Revision {
		t.Fatalf("second published state = %#v", second)
	}
	for _, operation := range []ReadStateOperation{ReadStateOperationRead, ReadStateOperationUnread} {
		outcome, err := runtime.UpdateThreadReadState(ctx, "read-state-thread", first.Revision, operation)
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Kind != ReadStateUpdateConflict || !sameThreadReadState(outcome.State, second) {
			t.Fatalf("%s with stale revision = %#v, want conflict with %#v", operation, outcome, second)
		}
	}
	acknowledged, err := runtime.UpdateThreadReadState(ctx, "read-state-thread", second.Revision, ReadStateOperationRead)
	if err != nil {
		t.Fatal(err)
	}
	if acknowledged.Kind != ReadStateUpdateApplied || acknowledged.State.FirstUnreadTurn != nil {
		t.Fatalf("acknowledge both results = %#v", acknowledged)
	}
	if changed, err := runtime.PublishThreadAttention(ctx, "read-state-thread", "turn-2"); err != nil || changed {
		t.Fatalf("publish after acknowledgement = %v, %v", changed, err)
	}
	if got := mustThreadReadState(t, ctx, runtime, "read-state-thread"); !sameThreadReadState(got, acknowledged.State) {
		t.Fatalf("acknowledged state = %#v, want %#v", got, acknowledged.State)
	}
}

// TestThreadReadStateSurvivesMetadataRebuildRestartAndDeleteLikeRust mirrors
// the second Rust state test: receipts are independent of rebuildable thread
// metadata, survive a runtime restart, and are removed with the thread.
func TestThreadReadStateSurvivesMetadataRebuildRestartAndDeleteLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(ctx, config, "default-provider")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 18, 11, 0, 0, 0, time.UTC)
	path := writeBackfillRollout(t, home, "durable-read-state-thread", now, false)
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	if changed, err := runtime.PublishThreadAttention(ctx, "durable-read-state-thread", "removed-turn"); err != nil || !changed {
		t.Fatalf("publish removed-turn = %v, %v", changed, err)
	}
	before := mustThreadReadState(t, ctx, runtime, "durable-read-state-thread")
	removed, err := runtime.UpdateThreadReadState(ctx, "durable-read-state-thread", before.Revision, ReadStateOperationRead)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Kind != ReadStateUpdateApplied || removed.State.FirstUnreadTurn != nil {
		t.Fatalf("retire removed turn = %#v", removed)
	}

	if _, err := runtime.StateDB().ExecContext(ctx, `DELETE FROM threads WHERE id = ?`, "durable-read-state-thread"); err != nil {
		t.Fatal(err)
	}
	if states, err := runtime.ThreadReadStates(ctx, []string{"durable-read-state-thread"}); err != nil || len(states) != 0 {
		t.Fatalf("states after metadata delete = %#v, %v", states, err)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := InitStateRuntime(ctx, config, "default-provider")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if got := mustThreadReadState(t, ctx, reopened, "durable-read-state-thread"); !sameThreadReadState(got, removed.State) {
		t.Fatalf("state after rebuild/restart = %#v, want %#v", got, removed.State)
	}
	if changed, err := reopened.PublishThreadAttention(ctx, "durable-read-state-thread", "removed-turn"); err != nil || changed {
		t.Fatalf("duplicate removed-turn after restart = %v, %v", changed, err)
	}
	if changed, err := reopened.PublishThreadAttention(ctx, "durable-read-state-thread", "new-live-turn"); err != nil || !changed {
		t.Fatalf("publish new-live-turn = %v, %v", changed, err)
	}
	live := mustThreadReadState(t, ctx, reopened, "durable-read-state-thread")
	if live.FirstUnreadTurn == nil || *live.FirstUnreadTurn != "new-live-turn" {
		t.Fatalf("live state = %#v", live)
	}
	if _, err := reopened.DeleteThreadsStrict(ctx, []string{"durable-read-state-thread"}); err != nil {
		t.Fatal(err)
	}
	if changed, err := reopened.PublishThreadAttention(ctx, "durable-read-state-thread", "late"); err != nil || changed {
		t.Fatalf("publish after delete = %v, %v", changed, err)
	}
	unavailable, err := reopened.UpdateThreadReadState(ctx, "durable-read-state-thread", removed.State.Revision, ReadStateOperationUnread)
	if err != nil {
		t.Fatal(err)
	}
	if unavailable.Kind != ReadStateUpdateUnavailable {
		t.Fatalf("update after delete = %#v", unavailable)
	}
	var receipts int
	if err := reopened.StateDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM thread_read_receipts WHERE thread_id = ?`, "durable-read-state-thread").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 {
		t.Fatalf("deleted thread kept %d receipts", receipts)
	}
}

func mustThreadReadState(t *testing.T, ctx context.Context, runtime *StateRuntime, threadID string) ThreadReadState {
	t.Helper()
	states, err := runtime.ThreadReadStates(ctx, []string{threadID})
	if err != nil {
		t.Fatal(err)
	}
	state, ok := states[threadID]
	if !ok {
		t.Fatalf("missing read state for %s", threadID)
	}
	return state
}

func sameThreadReadState(a, b ThreadReadState) bool {
	if a.Revision != b.Revision {
		return false
	}
	if a.FirstUnreadTurn == nil || b.FirstUnreadTurn == nil {
		return a.FirstUnreadTurn == nil && b.FirstUnreadTurn == nil
	}
	return *a.FirstUnreadTurn == *b.FirstUnreadTurn
}
