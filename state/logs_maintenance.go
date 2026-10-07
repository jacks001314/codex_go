package state

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// Mirrors Rust `logs_maintenance.rs` (upstream 8ea2c0e0d4 / #49425): prune
// diagnostic logs in the background immediately, then on a timer.
const (
	// logRetentionSeconds is the initial age window (Rust LOG_RETENTION_SECONDS).
	logRetentionSeconds int64 = 10 * 24 * 60 * 60
	// logDatabaseBudgetBytes bounds the occupied pages of the logs database
	// (Rust LOG_DATABASE_BUDGET_BYTES).
	logDatabaseBudgetBytes int64 = 64 * 1024 * 1024
	// logsMaintenancePeriod is how often the sweep repeats.
	logsMaintenancePeriod = 30 * time.Minute
)

// startPeriodicLogsMaintenance prunes diagnostic logs immediately and then every
// period, until the runtime is closed.
//
// Documented difference: Rust spawns the task from a `Weak<StateRuntime>` so the
// wait holds no strong reference; Go's goroutine owns the runtime and exits
// through the done channel that Close signals.
func (r *StateRuntime) startPeriodicLogsMaintenance(period time.Duration) {
	if r == nil || r.logsDB == nil {
		return
	}
	if period <= 0 {
		period = logsMaintenancePeriod
	}
	// Rust spawns one task per call (`start_periodic_logs_maintenance`); tests
	// rely on that to install a short period next to the startup task.
	r.logsMaintenanceMu.Lock()
	if r.logsMaintenanceDone == nil {
		r.logsMaintenanceDone = make(chan struct{})
	}
	done := r.logsMaintenanceDone
	select {
	case <-done:
		// The runtime is closing; do not start another sweep.
		r.logsMaintenanceMu.Unlock()
		return
	default:
	}
	// Registering under the lock keeps Add from racing Close's Wait.
	r.logsMaintenanceWG.Add(1)
	r.logsMaintenanceMu.Unlock()
	go func() {
		defer r.logsMaintenanceWG.Done()
		r.runLogsMaintenance(done, period)
	}()
}

func (r *StateRuntime) runLogsMaintenance(done <-chan struct{}, period time.Duration) {
	ctx := context.Background()
	firstSweep := true
	for {
		select {
		case <-done:
			return
		default:
		}
		if err := pruneLogsByAgeAndSize(ctx, r.logsDB, time.Now().Unix(), logDatabaseBudgetBytes); err != nil {
			slog.Warn("failed to prune diagnostic logs", "error", err)
		}
		if firstSweep {
			firstSweep = false
			// Preserve the startup checkpoint without waiting for readers or writers.
			if _, err := r.logsDB.ExecContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`); err != nil {
				slog.Warn("failed to checkpoint diagnostic logs", "error", err)
			}
		}
		select {
		case <-done:
			return
		case <-time.After(period):
		}
	}
}

// pruneLogsByAgeAndSize deletes logs older than the retention window, halving
// the window until the occupied pages fit the budget or the window reaches one
// second. The newest second is kept even when it exceeds the budget, and free
// pages are excluded from the measurement so SQLite can reuse them.
func pruneLogsByAgeAndSize(ctx context.Context, db *sql.DB, now int64, budgetBytes int64) error {
	if db == nil {
		return fmt.Errorf("logs database is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	retentionSeconds := logRetentionSeconds
	for {
		if _, err := db.ExecContext(ctx, `DELETE FROM logs WHERE ts < ?`, now-retentionSeconds); err != nil {
			return fmt.Errorf("delete expired logs: %w", err)
		}
		var occupiedBytes int64
		if err := db.QueryRowContext(ctx, `SELECT (page_count - freelist_count) * page_size FROM pragma_page_count(), pragma_freelist_count(), pragma_page_size()`).Scan(&occupiedBytes); err != nil {
			return fmt.Errorf("measure logs database: %w", err)
		}
		if occupiedBytes <= budgetBytes || retentionSeconds == 1 {
			return nil
		}
		if next := retentionSeconds / 2; next > 1 {
			retentionSeconds = next
		} else {
			// Keep the newest second even if it exceeds the budget.
			retentionSeconds = 1
		}
	}
}
