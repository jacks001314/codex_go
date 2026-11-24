package state

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	"codex_go/rollout"
)

// Rust #63456: a rollback-journal pool (a network filesystem keeps DELETE
// journaling) caps at two connections and waits 60 seconds for SQLite locks,
// while the WAL default stays at five connections and five seconds.
func TestRollbackJournalPoolSettingsLikeRust(t *testing.T) {
	ctx := context.Background()
	for _, testCase := range []struct {
		name    string
		nfs     bool
		journal string
		busy    int64
		conns   int
	}{
		{name: "network filesystem", nfs: true, journal: "delete", busy: 60000, conns: sqliteRollbackJournalMaxOpenConnections},
		{name: "local disk", nfs: false, journal: "wal", busy: sqliteBusyTimeoutMS, conns: SQLiteMaxOpenConnections},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			config, err := sqliteConfigWithFilesystemProbe(home, func(string) (bool, error) {
				return testCase.nfs, nil
			})
			if err != nil {
				t.Fatalf("sqliteConfigWithFilesystemProbe: %v", err)
			}
			if got := config.PrefersRollbackJournal(); got != testCase.nfs {
				t.Fatalf("PrefersRollbackJournal() = %v, want %v", got, testCase.nfs)
			}
			db, err := config.OpenReadWrite(ctx, config.LogsDBPath())
			if err != nil {
				t.Fatalf("OpenReadWrite: %v", err)
			}
			defer func() { _ = db.Close() }()
			if got := db.Stats().MaxOpenConnections; got != testCase.conns {
				t.Fatalf("MaxOpenConnections = %d, want %d", got, testCase.conns)
			}
			// Every pooled connection carries the setting, not just the first.
			connections := make([]*sql.Conn, 0, testCase.conns)
			for i := 0; i < testCase.conns; i++ {
				conn, err := db.Conn(ctx)
				if err != nil {
					t.Fatalf("acquire connection %d: %v", i, err)
				}
				connections = append(connections, conn)
			}
			defer func() {
				for _, conn := range connections {
					_ = conn.Close()
				}
			}()
			for i, conn := range connections {
				var journal string
				if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil {
					t.Fatalf("connection %d journal_mode: %v", i, err)
				}
				if journal != testCase.journal {
					t.Fatalf("connection %d journal_mode = %q, want %q", i, journal, testCase.journal)
				}
				var busy int64
				if err := conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busy); err != nil {
					t.Fatalf("connection %d busy_timeout: %v", i, err)
				}
				if busy != testCase.busy {
					t.Fatalf("connection %d busy_timeout = %d, want %d", i, busy, testCase.busy)
				}
			}
		})
	}
}

// Rust #63456: a batch another reader already partly committed replays its
// covered prefix and applies only the new suffix, and a fully covered batch
// commits nothing.
func TestThreadHistoryProjectionReplaysCommittedPrefixLikeRust(t *testing.T) {
	db, path := openProjectionFixture(t, historyMetaLine(0)+historyTurnLine(1, "turn-1", "2025-01-01T00:00:00Z"))
	assertMaterialized(t, db, path)
	before := requireProjectionState(t, db)

	appendProjectionFixture(t, path, historyTurnLine(2, "turn-2", "2025-01-01T00:01:00Z"))
	steps, nextOffset, err := rollout.ReadProjectionSteps(path, 0, 0)
	if err != nil {
		t.Fatalf("ReadProjectionSteps: %v", err)
	}
	// The caller read from the beginning while the checkpoint already covered
	// the first two lines.
	if err := applyThreadHistoryProjection(context.Background(), db, nil, "thread-1", path, 0, nextOffset, 0, nil, steps); err != nil {
		t.Fatalf("replayed projection error = %v", err)
	}
	after := requireProjectionState(t, db)
	if after.NextRolloutOrdinal != 3 || after.NextRolloutByteOffset != before.NextRolloutByteOffset+uint64(len(historyTurnLine(2, "turn-2", "2025-01-01T00:01:00Z"))) {
		t.Fatalf("projection state = %+v, want the second turn projected once", after)
	}
	if turns := countProjectedTurns(t, db); turns != 2 {
		t.Fatalf("projected turns = %d, want 2", turns)
	}

	// Replaying the whole file again changes nothing.
	if err := applyThreadHistoryProjection(context.Background(), db, nil, "thread-1", path, 0, nextOffset, 0, nil, steps); err != nil {
		t.Fatalf("fully covered projection error = %v", err)
	}
	if got := requireProjectionState(t, db); got != after {
		t.Fatalf("projection state = %+v, want %+v", got, after)
	}
}

// Rust #63456: a batch read before deletion must not recreate the history once
// the rollout is gone and its checkpoint cleared.
func TestThreadHistoryProjectionRejectsStaleBatchLikeRust(t *testing.T) {
	db, path := openProjectionFixture(t, historyMetaLine(0)+historyTurnLine(1, "turn-1", "2025-01-01T00:00:00Z"))
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove rollout: %v", err)
	}
	err := applyThreadHistoryProjection(context.Background(), db, nil, "thread-1", path, 0, 3, 0, nil, nil)
	if !errors.Is(err, ErrThreadHistoryNotFound) {
		t.Fatalf("stale batch error = %v, want ErrThreadHistoryNotFound", err)
	}
	if turns := countProjectedTurns(t, db); turns != 0 {
		t.Fatalf("projected turns = %d, want none", turns)
	}
}
