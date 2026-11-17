package state

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// Rust parity: codex-rs/thread-store/src/local/thread_history_materialization.rs
// at #62085. A flush whose plain rollout length already matches the saved
// projection returns without opening the rollout or reading its header.
func TestMaterializeThreadHistoryDurableSuffixSkipsCoveredProjectionLikeRust(t *testing.T) {
	db, path := openProjectionFixture(t, historyMetaLine(0)+historyTurnLine(1, "turn-1", "2025-01-01T00:00:00Z"))
	assertMaterialized(t, db, path)
	before := requireProjectionState(t, db)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.NextRolloutByteOffset != uint64(info.Size()) {
		t.Fatalf("projection offset = %d, file size = %d", before.NextRolloutByteOffset, info.Size())
	}

	// Keep the length identical but make the header unreadable: a materialization
	// that opened the rollout would now fail.
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), int(info.Size())), 0o600); err != nil {
		t.Fatal(err)
	}
	flushedLen := uint64(info.Size())
	if err := MaterializeThreadHistoryDurableSuffix(context.Background(), db, "thread-1", path, &flushedLen); err != nil {
		t.Fatalf("MaterializeThreadHistoryDurableSuffix() error = %v", err)
	}
	if after := requireProjectionState(t, db); after != before {
		t.Fatalf("projection state = %+v, want %+v", after, before)
	}
	if turns := countProjectedTurns(t, db); turns != 1 {
		t.Fatalf("projected turns = %d, want 1", turns)
	}
}

// Rust parity: codex-rs/thread-store/src/local/thread_history_materialization.rs
// at #62085. A flush still catches up the durable suffix of plain rollouts, and
// compressed rollouts are read because their file size is not a JSONL offset.
func TestMaterializeThreadHistoryDurableSuffixProjectsPlainAndCompressedSuffixLikeRust(t *testing.T) {
	ctx := context.Background()
	db, path := openProjectionFixture(t, historyMetaLine(0)+historyTurnLine(1, "turn-1", "2025-01-01T00:00:00Z"))
	assertMaterialized(t, db, path)

	appendProjectionFixture(t, path, historyTurnLine(2, "turn-2", "2025-01-01T00:00:01Z"))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	plainLen := uint64(info.Size())
	if err := MaterializeThreadHistoryDurableSuffix(ctx, db, "thread-1", path, &plainLen); err != nil {
		t.Fatalf("MaterializeThreadHistoryDurableSuffix(plain) error = %v", err)
	}
	if projected := requireProjectionState(t, db); projected.NextRolloutByteOffset != plainLen || projected.NextRolloutOrdinal != 3 {
		t.Fatalf("plain projection state = %+v, want offset %d ordinal 3", projected, plainLen)
	}
	assertProjectedTurnOrdinal(t, db, "turn-2", 2)

	appendProjectionFixture(t, path, historyTurnLine(3, "turn-3", "2025-01-01T00:00:02Z"))
	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	decodedLen := uint64(info.Size())
	compressedPath := compressProjectionFixture(t, path)
	if err := MaterializeThreadHistoryDurableSuffix(ctx, db, "thread-1", compressedPath, nil); err != nil {
		t.Fatalf("MaterializeThreadHistoryDurableSuffix(compressed) error = %v", err)
	}
	if projected := requireProjectionState(t, db); projected.NextRolloutByteOffset != decodedLen || projected.NextRolloutOrdinal != 4 {
		t.Fatalf("compressed projection state = %+v, want decoded offset %d ordinal 4", projected, decodedLen)
	}
	assertProjectedTurnOrdinal(t, db, "turn-3", 3)
}

func countProjectedTurns(t *testing.T, db *sql.DB) int {
	t.Helper()
	var turns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM thread_turns WHERE thread_id = ?`, "thread-1").Scan(&turns); err != nil {
		t.Fatalf("count projected turns: %v", err)
	}
	return turns
}

func assertProjectedTurnOrdinal(t *testing.T, db *sql.DB, turnID string, want int64) {
	t.Helper()
	var ordinal int64
	err := db.QueryRow(`SELECT rollout_ordinal FROM thread_turns WHERE thread_id = ? AND turn_id = ?`, "thread-1", turnID).Scan(&ordinal)
	if err != nil {
		t.Fatalf("read projected turn %q: %v", turnID, err)
	}
	if ordinal != want {
		t.Fatalf("projected turn %q ordinal = %d, want %d", turnID, ordinal, want)
	}
}

func compressProjectionFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	compressedPath := path + ".zst"
	file, err := os.Create(compressedPath)
	if err != nil {
		t.Fatalf("Create(%s): %v", compressedPath, err)
	}
	encoder, err := zstd.NewWriter(file)
	if err != nil {
		_ = file.Close()
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := encoder.Write(data); err != nil {
		_ = encoder.Close()
		_ = file.Close()
		t.Fatalf("compress rollout: %v", err)
	}
	if err := encoder.Close(); err != nil {
		_ = file.Close()
		t.Fatalf("close encoder: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close compressed rollout: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove(%s): %v", path, err)
	}
	return compressedPath
}
