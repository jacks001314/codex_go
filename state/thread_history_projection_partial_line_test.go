package state

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestMaterializeThreadHistoryPreservesTrailingPartialLineBoundariesLikeRust
// mirrors Rust #54639's extended
// `catch_up_preserves_trailing_partial_line_boundaries`
// (thread-store/src/local/thread_history_materialization_tests.rs): 64 KiB of
// padding per record keeps the byte offsets of a streamed record honest, and a
// record whose newline is not durable yet stays pending until a later pass
// projects it and closes the turn.
func TestMaterializeThreadHistoryPreservesTrailingPartialLineBoundariesLikeRust(t *testing.T) {
	db, path := openProjectionFixture(t, historyMetaLine(0))
	assertMaterialized(t, db, path)
	before := requireProjectionState(t, db)

	padding := strings.Repeat(" ", 64*1024)
	completeSuffix := strings.TrimSuffix(historyTurnLine(1, "turn-1", "2025-01-01T00:00:00Z"), "\n") + padding + "\n"
	partialLine := strings.TrimSuffix(historyTurnCompletedLine(2, "turn-1", "2025-01-01T00:00:00Z"), "\n") + padding
	appendProjectionFixture(t, path, completeSuffix+partialLine)

	assertMaterialized(t, db, path)
	state := requireProjectionState(t, db)
	if want := before.NextRolloutByteOffset + uint64(len(completeSuffix)); state.NextRolloutByteOffset != want {
		t.Fatalf("checkpoint offset = %d, want %d (the partial record stays pending)", state.NextRolloutByteOffset, want)
	}
	if state.NextRolloutOrdinal != 2 {
		t.Fatalf("checkpoint ordinal = %d, want 2", state.NextRolloutOrdinal)
	}
	var pendingEndOrdinal, pendingEndOffset any
	if err := db.QueryRow(`SELECT rollout_end_ordinal, rollout_end_byte_offset FROM thread_turns WHERE thread_id = ? AND turn_id = ?`,
		"thread-1", "turn-1").Scan(&pendingEndOrdinal, &pendingEndOffset); err != nil {
		t.Fatal(err)
	}
	if pendingEndOrdinal != nil || pendingEndOffset != nil {
		t.Fatalf("pending turn end = (%v,%v), want an unfinished turn row", pendingEndOrdinal, pendingEndOffset)
	}

	appendProjectionFixture(t, path, "\n")
	assertMaterialized(t, db, path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	state = requireProjectionState(t, db)
	if state.NextRolloutByteOffset != uint64(info.Size()) || state.NextRolloutOrdinal != 3 {
		t.Fatalf("completed checkpoint = %+v, want offset %d ordinal 3", state, info.Size())
	}
	var ordinal, endOrdinal, endOffset int64
	if err := db.QueryRow(`SELECT rollout_ordinal, rollout_end_ordinal, rollout_end_byte_offset FROM thread_turns WHERE thread_id = ? AND turn_id = ?`,
		"thread-1", "turn-1").Scan(&ordinal, &endOrdinal, &endOffset); err != nil {
		t.Fatal(err)
	}
	if ordinal != 1 || endOrdinal != 2 || endOffset != info.Size() {
		t.Fatalf("turn position = (%d,%d,%d), want (1,2,%d)", ordinal, endOrdinal, endOffset, info.Size())
	}
}

func historyTurnCompletedLine(ordinal uint64, turnID string, timestamp string) string {
	return fmt.Sprintf(`{"timestamp":%q,"ordinal":%d,"type":"event_msg","payload":{"type":"turn_complete","turn_id":%q,"started_at":10,"completed_at":20}}`, timestamp, ordinal, turnID) + "\n"
}
