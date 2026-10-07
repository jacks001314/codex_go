package state

// Background reclamation of unused SQLite pages, mirroring Rust
// `state/src/runtime/reclamation.rs` (Rust #49069, upstream 33a0f766a6).
//
// Deleting rows leaves free SQLite pages available for reuse without shrinking
// the database file. The worker incrementally vacuums the databases that opted
// in while preserving a reserve for foreground writers. A per-home file lock
// elects one worker per home, and shutdown waits for the dedicated connection
// to close before the worker releases the lock.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	modernsqlite "modernc.org/sqlite"
)

const (
	reclamationIdleInterval       = 60 * time.Second
	reclamationActiveInterval     = 50 * time.Millisecond
	reclamationContentionInterval = 500 * time.Millisecond
	reclamationPassDuration       = 100 * time.Millisecond
	reclamationMinFreeBytes       = 64 * 1024 * 1024
	reclamationReserveBytes       = 16 * 1024 * 1024
	reclamationPassPages          = 1024
	reclamationBatchPages         = 64
	// reclamationBatchPause is Rust's 5ms pause between committed batches.
	reclamationBatchPause = 5 * time.Millisecond
	// reclamationLockFile is the per-home lock that elects one active worker
	// (Rust `try_ownership`). Holders keep it for the lifetime of the worker.
	reclamationLockFile = ".sqlite-maintenance.lock"
)

// reclamationTarget is one database that opted into background reclamation.
type reclamationTarget struct {
	label string
	path  string
}

// reclamationOutcome mirrors Rust `PassOutcome`.
type reclamationOutcome int

const (
	reclamationOutcomeIdle reclamationOutcome = iota
	reclamationOutcomeActive
	reclamationOutcomeContended
	reclamationOutcomeInterrupted
	reclamationOutcomeShutdown
)

// reclamationPass mirrors Rust `ReclamationPass`.
type reclamationPass struct {
	pages   uint32
	outcome reclamationOutcome
}

// reclamationResult carries one pass plus the error Rust models as
// `anyhow::Result<ReclamationPass>` (the scheduler takes both).
type reclamationResult struct {
	pass reclamationPass
	err  error
}

// reclamationBudget mirrors Rust `Budget`: a cooperative deadline and a page
// bound for one pass.
type reclamationBudget struct {
	deadline time.Time
	pages    uint32
}

// reclamationOptions mirrors Rust `ReclamationOptions`.
type reclamationOptions struct {
	budget     reclamationBudget
	batchPages uint32
}

// reclamationState mirrors Rust `ReclamationState`: the batch size shrinks
// after a stalled pass so the next attempt is less likely to be interrupted.
type reclamationState struct {
	batchPages    uint32
	stalledPasses uint32
}

// reclamationAdmission mirrors the admission check in Rust `reclaim_pages`:
// a pass only reclaims when incremental auto-vacuum is enabled and at least
// 64 MiB and 25% of the database are free pages. Returned as a predicate so the
// decision stays testable without a database.
func reclamationAdmission(autoVacuum, pageSize, pages, free int64) bool {
	return autoVacuum == 2 && free*pageSize >= reclamationMinFreeBytes && free >= pages/4
}

// sqliteReclamationWorker mirrors Rust `SqliteReclamationWorker`: one goroutine
// per runtime, elected per home, visiting the opted-in databases serially on
// individual retry schedules.
type sqliteReclamationWorker struct {
	shutdown chan struct{}
	finished chan struct{}
	closeOne sync.Once

	home    string
	targets []reclamationTarget

	// Test seams; production leaves them unset. `probe` performs one pass and
	// `idleInterval` replaces Rust's IDLE_INTERVAL for the first wake-up and for
	// the schedule floor, so a test can drive scheduling without a database.
	probe        func(ctx context.Context, target reclamationTarget, options reclamationOptions, shutdown <-chan struct{}) reclamationResult
	idleInterval time.Duration

	// metrics receives one sample per pass (Rust `record_reclamation`).
	metrics *TaskMetrics
}

// newSqliteReclamationWorker mirrors Rust `SqliteReclamationWorker::spawn`'s
// construction: the worker is created only when at least one database opted in.
func newSqliteReclamationWorker(config SqliteConfig) *sqliteReclamationWorker {
	targets := config.reclamationTargets()
	if len(targets) == 0 {
		return nil
	}
	return &sqliteReclamationWorker{
		shutdown: make(chan struct{}),
		finished: make(chan struct{}),
		home:     config.Home(),
		targets:  targets,
		metrics:  config.reclamationMetrics,
	}
}

// spawnSqliteReclamationWorker starts background reclamation for the databases
// that opted in and returns its handle (nil when there are none).
func spawnSqliteReclamationWorker(config SqliteConfig) *sqliteReclamationWorker {
	worker := newSqliteReclamationWorker(config)
	if worker == nil {
		return nil
	}
	go worker.run()
	return worker
}

// close stops the worker and waits for the goroutine, including an in-flight
// pass, to finish. Rust waits on `finished.changed()` so every caller, not just
// the first, observes completion; closing the channel does the same here.
func (w *sqliteReclamationWorker) close() {
	if w == nil {
		return
	}
	w.closeOne.Do(func() { close(w.shutdown) })
	<-w.finished
}

// run mirrors Rust's spawned task: sleep, elect one owner per home, then visit
// every database whose retry deadline passed.
func (w *sqliteReclamationWorker) run() {
	defer close(w.finished)
	scheduled := make([]time.Time, len(w.targets))
	states := make([]reclamationState, len(w.targets))
	for i := range w.targets {
		scheduled[i] = time.Now()
		states[i] = reclamationState{batchPages: reclamationBatchPages}
	}
	var owner *flock.Flock
	defer func() {
		if owner != nil {
			_ = owner.Unlock()
		}
	}()
	delay := w.idle()
	for {
		select {
		case <-w.shutdown:
			return
		case <-time.After(delay):
		}
		if owner == nil {
			owned, err := tryReclamationOwnership(w.home)
			if err != nil {
				// Rust discards the error (`.ok().flatten()`): a home whose lock
				// file cannot be opened simply never reclaims.
				slog.Debug("sqlite reclamation could not take the maintenance lock", "error", err)
				continue
			}
			if owned == nil {
				// Another worker owns reclamation for this home.
				continue
			}
			owner = owned
		}
		delay = w.visit(scheduled, states)
	}
}

// visit mirrors Rust `visit`: reclaim every due database, then sleep until the
// earliest remaining deadline (IDLE_INTERVAL when nothing is scheduled).
func (w *sqliteReclamationWorker) visit(scheduled []time.Time, states []reclamationState) time.Duration {
	for i, target := range w.targets {
		started := time.Now()
		if w.stopped() {
			break
		}
		if scheduled[i].After(started) {
			continue
		}
		result := w.probeFn()(context.Background(), target, reclamationOptions{
			budget: reclamationBudget{
				deadline: started.Add(reclamationPassDuration),
				pages:    reclamationPassPages,
			},
			batchPages: states[i].batchPages,
		}, w.shutdown)
		recordReclamation(w.metrics, target.label, time.Since(started), result)
		scheduled[i] = time.Now().Add(states[i].retryAfter(result))
	}
	delay := w.idle()
	for i := range scheduled {
		if remaining := time.Until(scheduled[i]); remaining < delay {
			delay = remaining
		}
	}
	if delay < 0 {
		delay = 0
	}
	return delay
}

func (w *sqliteReclamationWorker) probeFn() func(context.Context, reclamationTarget, reclamationOptions, <-chan struct{}) reclamationResult {
	if w.probe != nil {
		return w.probe
	}
	return reclaimRuntimeDB
}

// stopped reports whether shutdown was requested; Rust checks the shutdown
// receiver between databases and inside a pass.
func (w *sqliteReclamationWorker) stopped() bool {
	select {
	case <-w.shutdown:
		return true
	default:
		return false
	}
}

// idle is Rust's IDLE_INTERVAL, overridable by tests.
func (w *sqliteReclamationWorker) idle() time.Duration {
	if w.idleInterval > 0 {
		return w.idleInterval
	}
	return reclamationIdleInterval
}

// retryAfter mirrors Rust `ReclamationState::retry_after`. An interrupted pass
// that reclaimed nothing halves the batch and backs off exponentially from the
// contention interval up to the idle interval; otherwise the outcome picks the
// next delay and a smaller batch is kept because growing it would repeat the
// stall.
func (s *reclamationState) retryAfter(result reclamationResult) time.Duration {
	if result.err == nil && result.pass.outcome == reclamationOutcomeInterrupted && result.pass.pages == 0 {
		if s.stalledPasses < 1<<31 {
			s.stalledPasses++
		}
		if half := s.batchPages / 2; half >= 1 {
			s.batchPages = half
		} else {
			s.batchPages = 1
		}
		shift := s.stalledPasses - 1
		if shift > 7 {
			shift = 7
		}
		delay := reclamationContentionInterval << shift
		if delay > reclamationIdleInterval {
			delay = reclamationIdleInterval
		}
		return delay
	}
	s.stalledPasses = 0
	if result.err != nil {
		return reclamationIdleInterval
	}
	switch result.pass.outcome {
	case reclamationOutcomeActive, reclamationOutcomeInterrupted:
		return reclamationActiveInterval
	case reclamationOutcomeContended:
		return reclamationContentionInterval
	default:
		return reclamationIdleInterval
	}
}

// tryReclamationOwnership mirrors Rust `try_ownership`: a held lock means
// another worker owns reclamation for this home and returns nil, while only a
// real I/O failure returns an error.
func tryReclamationOwnership(home string) (*flock.Flock, error) {
	lock := flock.New(filepath.Join(home, reclamationLockFile))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, nil
	}
	return lock, nil
}

// reclaimRuntimeDB mirrors Rust `reclaim`: skip a database that does not exist,
// open the dedicated maintenance connection, run one pass and close it. Because
// the connection is dedicated and closed here, an interrupted transaction can
// never leak into foreground work.
func reclaimRuntimeDB(ctx context.Context, target reclamationTarget, options reclamationOptions, shutdown <-chan struct{}) reclamationResult {
	if _, err := os.Stat(target.path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Rust never creates the database from the maintenance connection.
			return reclamationResult{pass: reclamationPass{outcome: reclamationOutcomeIdle}}
		}
		return reclamationResult{err: err}
	}
	db, err := openReclamationDB(ctx, target.path)
	if err != nil {
		// Rust maps a failing connect through `outcome_after_error` as well.
		if outcome, ok := reclamationOutcomeAfterError(err); ok {
			return reclamationResult{pass: reclamationPass{outcome: outcome}}
		}
		return reclamationResult{err: err}
	}
	defer func() { _ = db.Close() }()
	conn, err := db.Conn(ctx)
	if err != nil {
		return reclamationResult{err: err}
	}
	defer func() { _ = conn.Close() }()
	pass, err := reclaimPages(ctx, conn, options, shutdown)
	if err != nil {
		return reclamationResult{err: err}
	}
	return reclamationResult{pass: pass}
}

// reclamationReservePages mirrors the reserve Rust computes before reclaiming:
// at least 16 MiB or a tenth of the database stays free for foreground writers.
func reclamationReservePages(pageSize, pages int64) int64 {
	reserve := reclamationReserveBytes / pageSize
	if pages/10 > reserve {
		reserve = pages / 10
	}
	return reserve
}

// reclaimPages mirrors Rust `reclaim_pages`: reclaim free pages in short
// transactions while preserving a reusable reserve.
//
// Documented differences from Rust: Rust installs a SQLite progress handler
// that aborts a running statement once the cooperative deadline passes or
// shutdown is requested. Go has no progress-handler hook, so the same deadline
// bounds each statement through its context, the loop re-checks the deadline and
// shutdown between batches, and an interrupted statement is classified into the
// same Interrupted outcome. A PASSIVE checkpoint never takes the writer lock, so
// the checkpoint runs without the deadline, which matches Rust tolerating a
// checkpoint that overruns it.
func reclaimPages(ctx context.Context, conn *sql.Conn, options reclamationOptions, shutdown <-chan struct{}) (reclamationPass, error) {
	stopped := func() bool {
		select {
		case <-shutdown:
			return true
		default:
			return false
		}
	}
	// Rust treats an absent deadline as "never interrupted".
	expired := func() bool {
		return !options.budget.deadline.IsZero() && !time.Now().Before(options.budget.deadline)
	}
	if expired() || stopped() {
		outcome := reclamationOutcomeInterrupted
		if stopped() {
			outcome = reclamationOutcomeShutdown
		}
		return reclamationPass{outcome: outcome}, nil
	}
	statementCtx := ctx
	if !options.budget.deadline.IsZero() {
		var cancel context.CancelFunc
		statementCtx, cancel = context.WithDeadline(ctx, options.budget.deadline)
		defer cancel()
	}
	reclaimed := uint32(0)
	// Rust starts a pass assuming contention; every exit path below sets the
	// outcome it observed.
	outcome := reclamationOutcomeContended
	err := func() error {
		var autoVacuum, pageSize, pages, free int64
		if err := conn.QueryRowContext(statementCtx, `SELECT auto_vacuum, page_size, page_count, freelist_count `+
			`FROM pragma_auto_vacuum(), pragma_page_size(), pragma_page_count(), pragma_freelist_count()`).
			Scan(&autoVacuum, &pageSize, &pages, &free); err != nil {
			return err
		}
		// Require incremental auto-vacuum and at least 64 MiB and 25% free space.
		if !reclamationAdmission(autoVacuum, pageSize, pages, free) {
			outcome = reclamationOutcomeIdle
			return nil
		}
		complete, err := checkpointComplete(ctx, conn)
		if err != nil || !complete {
			return err
		}
		outcome = reclamationOutcomeActive
		reserve := reclamationReservePages(pageSize, pages)
		version, err := reclamationDataVersion(ctx, conn)
		if err != nil {
			return err
		}
		for reclaimed < options.budget.pages && !expired() && !stopped() {
			// Check the reserve inside the same write transaction, so a
			// foreground writer cannot consume it between the free-page check
			// and the vacuum.
			if _, err := conn.ExecContext(statementCtx, `BEGIN IMMEDIATE`); err != nil {
				return err
			}
			free, err := reclamationFreelistCount(statementCtx, conn)
			if err != nil {
				return errors.Join(err, reclamationRollback(ctx, conn))
			}
			if free <= reserve {
				if err := reclamationRollback(ctx, conn); err != nil {
					return err
				}
				outcome = reclamationOutcomeIdle
				break
			}
			batch := int64(options.batchPages)
			if remaining := int64(options.budget.pages - reclaimed); remaining < batch {
				batch = remaining
			}
			if free-reserve < batch {
				batch = free - reserve
			}
			reclaimedPages, err := reclamationVacuumPages(statementCtx, conn, batch)
			if err != nil {
				return errors.Join(err, reclamationRollback(ctx, conn))
			}
			if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
				return err
			}
			reclaimed += reclaimedPages
			// A reader arriving after admission can pin at most one batch.
			complete, err := checkpointComplete(ctx, conn)
			if err != nil {
				return err
			}
			if !complete {
				outcome = reclamationOutcomeContended
				break
			}
			time.Sleep(reclamationBatchPause)
			// Back off if another connection committed during this pass.
			current, err := reclamationDataVersion(ctx, conn)
			if err != nil {
				return err
			}
			if current != version {
				outcome = reclamationOutcomeContended
				break
			}
		}
		return nil
	}()
	if err != nil {
		mapped, ok := reclamationOutcomeAfterError(err)
		if !ok {
			return reclamationPass{}, err
		}
		outcome = mapped
	}
	if stopped() {
		outcome = reclamationOutcomeShutdown
	} else if outcome == reclamationOutcomeActive && expired() {
		// The deadline can also expire between statements, without an interrupt.
		outcome = reclamationOutcomeInterrupted
	}
	return reclamationPass{pages: reclaimed, outcome: outcome}, nil
}

// reclamationVacuumPages reclaims up to pages free pages and reports how many
// SQLite removed.
//
// Documented driver difference from Rust: `PRAGMA incremental_vacuum(n)` yields
// one row per reclaimed page, and Rust's `execute` drives the statement to
// completion. Go's `ExecContext` stops after the first step, which removes a
// single page, so the rows must be drained here. Counting the drained rows also
// keeps `ReclamationPass.pages` equal to the pages SQLite actually removed.
func reclamationVacuumPages(ctx context.Context, conn *sql.Conn, pages int64) (uint32, error) {
	if pages <= 0 {
		return 0, nil
	}
	rows, err := conn.QueryContext(ctx, fmt.Sprintf("PRAGMA incremental_vacuum(%d)", pages))
	if err != nil {
		return 0, err
	}
	removed := uint32(0)
	for rows.Next() {
		removed++
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// reclamationRollback ends an open maintenance transaction; the deadline must
// not cancel the rollback itself.
func reclamationRollback(ctx context.Context, conn *sql.Conn) error {
	_, err := conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`)
	return err
}

// checkpointComplete mirrors Rust `checkpoint_complete`. PASSIVE does not take
// the writer lock or wait for readers, so a busy or incomplete checkpoint is
// contention to defer rather than a failure.
func checkpointComplete(ctx context.Context, conn *sql.Conn) (bool, error) {
	var busy, frames, done int64
	if err := conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &frames, &done); err != nil {
		return false, err
	}
	return busy == 0 && frames >= 0 && frames == done, nil
}

func reclamationFreelistCount(ctx context.Context, conn *sql.Conn) (int64, error) {
	var free int64
	if err := conn.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		return 0, err
	}
	return free, nil
}

func reclamationDataVersion(ctx context.Context, conn *sql.Conn) (int64, error) {
	var version int64
	if err := conn.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&version); err != nil {
		return 0, err
	}
	return version, nil
}

// reclamationOutcomeAfterError mirrors Rust `outcome_after_error`: SQLite primary
// result codes classify deferred work, while anything else is a real failure.
// Context cancellation stands in for Rust's progress-handler interrupt.
func reclamationOutcomeAfterError(err error) (reclamationOutcome, bool) {
	if err == nil {
		return reclamationOutcomeIdle, false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return reclamationOutcomeInterrupted, true
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case 5, 6: // SQLITE_BUSY, SQLITE_LOCKED
			return reclamationOutcomeContended, true
		case 9: // SQLITE_INTERRUPT
			return reclamationOutcomeInterrupted, true
		}
	}
	return reclamationOutcomeIdle, false
}

// openReclamationDB opens the dedicated maintenance connection Rust builds in
// `reclaim`: no busy wait, no automatic checkpointing, a 16 MiB cache and no
// statement logging. The caller closes it, so an interrupted transaction can
// never leak into foreground work.
func openReclamationDB(ctx context.Context, path string) (*sql.DB, error) {
	dsn, err := reclamationDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Rust uses one dedicated `SqliteConnection`; pin the pool to a single
	// connection so every statement sees the maintenance PRAGMAs.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// reclamationDSN mirrors the connection options Rust passes to
// `SqliteConnection::connect_with`: the file must already exist, busy_timeout is
// zero (contention defers the pass instead of waiting), commits must not run
// checkpoint I/O while holding up writers, and the larger cache keeps large
// vacuums cheap.
func reclamationDSN(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("sqlite database path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve sqlite database path: %w", err)
	}
	uriPath := filepath.ToSlash(absolute)
	if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := &url.URL{Scheme: "file", Path: uriPath}
	query := u.Query()
	// `mode=rw` never creates the database (Rust `create_if_missing(false)`).
	query.Set("mode", "rw")
	query.Add("_pragma", "busy_timeout(0)")
	query.Add("_pragma", "synchronous(NORMAL)")
	query.Add("_pragma", "wal_autocheckpoint(0)")
	query.Add("_pragma", "cache_size(-16384)")
	u.RawQuery = query.Encode()
	return u.String(), nil
}

// Metric names mirroring Rust `telemetry::record_reclamation` (upstream
// 33a0f766a6 / #49069).
const (
	reclamationCountMetric    = "codex.sqlite.reclamation.count"
	reclamationDurationMetric = "codex.sqlite.reclamation.duration_ms"
	reclamationPagesMetric    = "codex.sqlite.reclamation.pages"
)

// recordReclamation mirrors Rust `telemetry::record_reclamation`: one counter,
// one duration and — only for a pass that completed without an error — one page
// histogram, tagged with the database, the status and the classified error.
//
// Documented differences from Rust: Rust resolves the process-level telemetry
// sink and reports `success`/`failed`; Go threads the one `*TaskMetrics` instance
// the router already holds (leader ruling for #49701's stage C) and keeps Rust's
// tag values, which differ from the older `ok`/`error` pair used by
// `codex.sqlite.log.write`.
func recordReclamation(metrics *TaskMetrics, db string, duration time.Duration, result reclamationResult) {
	if metrics == nil {
		return
	}
	status, errorTag := "success", "none"
	if result.err != nil {
		status, errorTag = "failed", classifyReclamationError(result.err)
	}
	tags := map[string]string{"db": db, "status": status, "error": errorTag}
	metrics.Counter(reclamationCountMetric, 1, tags)
	metrics.RecordDuration(reclamationDurationMetric, duration, tags)
	if result.err == nil {
		metrics.Histogram(reclamationPagesMetric, int(result.pass.pages), tags)
	}
}

// classifyReclamationError mirrors Rust `classify_error`/`classify_sqlite_code`:
// the SQLite primary result code becomes a stable tag value, an interrupted
// statement is `interrupt` and loose I/O failures are `io`.
func classifyReclamationError(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "interrupt"
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & 0xff {
		case 5:
			return "busy"
		case 6:
			return "locked"
		case 8:
			return "readonly"
		case 10:
			return "io"
		case 11:
			return "corrupt"
		case 13:
			return "full"
		case 14:
			return "cantopen"
		case 17:
			return "schema"
		case 19:
			return "constraint"
		}
		return "unknown"
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return "io"
	}
	return "unknown"
}

// reclamationTargets returns the databases that opted into background
// reclamation (Rust `SqliteConfig::runtime_db_paths` filtered by
// `background_reclamation`). Only the logs database opts in: its transactions
// write before reading, so an intervening reclamation commit cannot make a
// deferred read-to-write upgrade fail with SQLITE_BUSY_SNAPSHOT.
func (c SqliteConfig) reclamationTargets() []reclamationTarget {
	var targets []reclamationTarget
	for _, spec := range runtimeDBSpecs {
		if spec.backgroundReclamation {
			targets = append(targets, reclamationTarget{label: spec.label, path: spec.path(c)})
		}
	}
	return targets
}
