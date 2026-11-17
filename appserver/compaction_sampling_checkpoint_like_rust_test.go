package appserver

import (
	"testing"

	"codex_go/session"
)

// TestRuntimeCompactionCheckpointsSamplingAgainstReplacedHistoryLikeRust
// mirrors Rust `Session::checkpoint_sampling`
// (`codex-rs/core/src/session/mod.rs`), which annotates a compaction
// replacement against the live history before installing it.
//
// After the swap the replacement's first item acknowledges the sampling prefix
// of the history it replaced, so a resumed thread does not treat rebuilt input
// as undelivered. Items the replacement introduces carry an append identity
// beyond the replaced window instead of colliding with recorded input.
func TestRuntimeCompactionCheckpointsSamplingAgainstReplacedHistoryLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	const threadID = "thread-compaction-sampling-checkpoint"
	items := []session.Item{
		{ID: "u1", Type: "message", Role: "user", Text: "first request"},
		{ID: "a1", Type: "agent_message", Role: "assistant", Text: "first answer"},
	}
	session.MergeItemHarnessMetadata(&items[0], map[string]any{
		"history_sequence":         uint64(1),
		"sampled_through_sequence": uint64(1),
	})
	session.MergeItemHarnessMetadata(&items[1], map[string]any{
		"history_sequence":         uint64(2),
		"sampled_through_sequence": uint64(2),
	})
	if err := store.Save(&session.Record{ID: threadID, SessionID: threadID, Items: items}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	router.SetNotificationSink(NewNotificationBuffer())
	router.requireThreadStatus().UpsertThread(threadID, false)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadCompactStart, ThreadCompactStartParams{ThreadID: threadID}))
	if response.Error != nil {
		t.Fatalf("compact error: %+v", response.Error)
	}

	record, err := store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(record.Items) == 0 {
		t.Fatal("compacted record has no items")
	}
	if got := session.ItemSampledThroughSequence(&record.Items[0]); got != 2 {
		t.Fatalf("first replacement sampled_through = %d, want the replaced window's checkpoint 2", got)
	}
	if got := session.MaxSampledThroughSequence(record.Items); got != 2 {
		t.Fatalf("replacement max sampled_through = %d, want 2 (compaction never acknowledges its own input)", got)
	}
	if got := session.ItemHistorySequence(&record.Items[0]); got <= 2 {
		t.Fatalf("first replacement history_sequence = %d, want an identity beyond the replaced window", got)
	}
}
