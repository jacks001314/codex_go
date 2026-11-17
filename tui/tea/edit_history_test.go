package tea

import "testing"

func draftSnapshot(value string) composerDraftSnapshot {
	return composerDraftSnapshot{value: value}
}

// A transaction records the pre-edit draft only when the text actually changed,
// and a new edit clears redo (Rust #61951 `EditHistory`).
func TestEditHistoryCommitRecordsChangesOnlyLikeRust(t *testing.T) {
	var history editHistory
	if !history.begin(draftSnapshot("same")) {
		t.Fatal("begin() reported an existing transaction")
	}
	if history.begin(draftSnapshot("nested")) {
		t.Fatal("a nested begin() opened a second transaction")
	}
	if history.commit(draftSnapshot("same")) {
		t.Fatal("commit() recorded an unchanged draft")
	}
	if len(history.undo) != 0 {
		t.Fatalf("undo steps = %d, want none", len(history.undo))
	}

	history.begin(draftSnapshot("before"))
	if !history.commit(draftSnapshot("after")) {
		t.Fatal("commit() dropped a real edit")
	}
	if len(history.undo) != 1 || history.undo[0].value != "before" {
		t.Fatalf("undo = %#v, want the pre-edit draft", history.undo)
	}

	history.redo = []composerDraftSnapshot{draftSnapshot("stale")}
	history.begin(draftSnapshot("after"))
	history.commit(draftSnapshot("newer"))
	if len(history.redo) != 0 {
		t.Fatalf("redo = %#v, want it cleared by a new edit", history.redo)
	}
}

// Stepping moves the current draft to the opposite stack so undo and redo
// round-trip the same values.
func TestEditHistoryStepTransfersSnapshotsLikeRust(t *testing.T) {
	var history editHistory
	history.begin(draftSnapshot("one"))
	history.commit(draftSnapshot("two"))
	history.begin(draftSnapshot("two"))
	history.commit(draftSnapshot("three"))

	snapshot, ok := history.step(editUndo, draftSnapshot("three"))
	if !ok || snapshot.value != "two" {
		t.Fatalf("undo = (%#v, %t), want two", snapshot, ok)
	}
	if len(history.undo) != 1 || len(history.redo) != 1 || history.redo[0].value != "three" {
		t.Fatalf("after undo: undo=%#v redo=%#v", history.undo, history.redo)
	}

	snapshot, ok = history.step(editRedo, draftSnapshot("two"))
	if !ok || snapshot.value != "three" {
		t.Fatalf("redo = (%#v, %t), want three", snapshot, ok)
	}
	if len(history.undo) != 2 || len(history.redo) != 0 {
		t.Fatalf("after redo: undo=%#v redo=%#v", history.undo, history.redo)
	}

	if _, ok := history.step(editRedo, draftSnapshot("three")); ok {
		t.Fatal("redo() succeeded with an empty stack")
	}
	if history.pending {
		t.Fatal("step() left a transaction pending")
	}
}

// The undo stack keeps only the newest steps, dropping the oldest first.
func TestEditHistoryBoundsUndoStepsLikeRust(t *testing.T) {
	var history editHistory
	for i := 0; i < maxEditHistorySteps+5; i++ {
		history.begin(draftSnapshot("value"))
		history.commit(draftSnapshot("value-" + string(rune('a'+i))))
	}
	if len(history.undo) != maxEditHistorySteps {
		t.Fatalf("undo steps = %d, want %d", len(history.undo), maxEditHistorySteps)
	}
	if got := history.undo[len(history.undo)-1].value; got != "value" {
		t.Fatalf("newest undo step = %q", got)
	}
	history.reset()
	if len(history.undo) != 0 || len(history.redo) != 0 || history.pending {
		t.Fatalf("reset left history behind: %#v", history)
	}
}
