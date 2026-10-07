package state

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// QuickCheckOutcome classifies a budgeted `PRAGMA quick_check(1)` scan.
//
// Mirrors Rust `QuickCheckOutcome`
// (codex-rs/state/src/sqlite/validation.rs, upstream 3620b2caf8 / #49701
// "Detect SQLite corruption during startup and preserve recovery backups").
type QuickCheckOutcome int

const (
	// QuickCheckComplete means the scan returned "ok".
	QuickCheckComplete QuickCheckOutcome = iota
	// QuickCheckIncomplete means the scan was interrupted by its budget or
	// failed with something other than a corruption error. Rust treats an
	// interrupted or locked scan as "not confirmed", never as corruption.
	QuickCheckIncomplete
	// QuickCheckCorruptedNeedsFixed means the scan reported corruption.
	QuickCheckCorruptedNeedsFixed
	// QuickCheckSkipped means this owner already attempted validation for the
	// file identity and did not scan it again.
	QuickCheckSkipped
)

func (o QuickCheckOutcome) String() string {
	switch o {
	case QuickCheckComplete:
		return "complete"
	case QuickCheckIncomplete:
		return "incomplete"
	case QuickCheckCorruptedNeedsFixed:
		return "corrupted_needs_fixed"
	case QuickCheckSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// DefaultQuickCheckBudget bounds startup validation, mirroring Rust's 100 ms
// quick-check budget (codex-rs/state/src/sqlite.rs, #49701).
const DefaultQuickCheckBudget = 100 * time.Millisecond

// sqliteQuickCheckManager tracks which database files were already validated.
//
// Rust keys the set on `file_id::FileId`. Go uses os.SameFile over the
// os.FileInfo values it has seen, which resolves to device+inode on Unix and
// the file index on Windows: replacing a database at the same path yields a new
// identity and is scanned again, exactly like the Rust manager.
//
// Attempts are recorded before the scan so a failed, interrupted or cancelled
// check still counts as attempted (Rust `quick_check_once` inserts the file id
// first).
type sqliteQuickCheckManager struct {
	mu   sync.Mutex
	seen []os.FileInfo
}

// markAttempt records the file identity and reports whether it is new to this
// manager.
func (m *sqliteQuickCheckManager) markAttempt(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, seen := range m.seen {
		if seen != nil && os.SameFile(seen, info) {
			return false, nil
		}
	}
	m.seen = append(m.seen, info)
	return true, nil
}

// quickCheckOnce returns this attempt's outcome, or QuickCheckSkipped when this
// owner already attempted validation for the file.
func (m *sqliteQuickCheckManager) quickCheckOnce(ctx context.Context, db *sql.DB, path string, budget time.Duration) (QuickCheckOutcome, error) {
	if m == nil {
		// Without a shared manager every open validates independently.
		return quickCheck(ctx, db, budget), nil
	}
	first, err := m.markAttempt(path)
	if err != nil {
		return QuickCheckIncomplete, err
	}
	if !first {
		return QuickCheckSkipped, nil
	}
	return quickCheck(ctx, db, budget), nil
}

// quickCheck runs `PRAGMA quick_check(1)` under the given scan budget.
//
// Rust enforces the budget with a SQLite progress handler that fires every
// 1_000 VM operations; Go enforces it with a deadline on the scan context (the
// modernc driver interrupts the statement when the context is done). A result
// row wins over an interrupt, so a finding that arrived before the budget
// expired is retained.
//
// Documented difference: with an already-expired budget Rust can still deliver
// an early finding (the progress handler needs 1_000 ops to fire), while a Go
// context deadline that has already passed aborts the statement before the
// driver produces any row. The realistic path (a positive budget) is
// equivalent.
func quickCheck(ctx context.Context, db *sql.DB, budget time.Duration) QuickCheckOutcome {
	if db == nil {
		return QuickCheckIncomplete
	}
	// The budget is the only limit: Rust's quick_check takes no caller
	// cancellation, so a cancelled startup context must not turn a definitive
	// scan into an Incomplete one.
	scanCtx, cancel := context.WithTimeout(context.WithoutCancel(nonNilContext(ctx)), budget)
	defer cancel()
	var result string
	err := db.QueryRowContext(scanCtx, "PRAGMA quick_check(1)").Scan(&result)
	switch {
	case err == nil && strings.TrimSpace(result) == "ok":
		return QuickCheckComplete
	case err == nil:
		return QuickCheckCorruptedNeedsFixed
	case IsSQLiteCorruptionError(err):
		return QuickCheckCorruptedNeedsFixed
	default:
		return QuickCheckIncomplete
	}
}

// quickCheckDatabase validates a freshly opened database with the config's
// shared manager.
func (c SqliteConfig) quickCheckDatabase(ctx context.Context, db *sql.DB, path string) (QuickCheckOutcome, error) {
	return c.quickCheck.quickCheckOnce(ctx, db, path, DefaultQuickCheckBudget)
}

func logQuickCheckCorruption(path string, label string) {
	slog.Error("sqlite quick check detected corruption", "database", path, "label", label)
}
