package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Mirrors Rust state/src/runtime/reclamation.rs and reclamation_tests.rs
// (Rust #49069, upstream 33a0f766a6). Rust covers `reclaim_pages` directly; Go
// keeps the same seams (`retry_after`, `try_ownership`, the spawned task) so the
// worker's ownership election, retry schedule and shutdown can be asserted
// without a multi-gigabyte database.

// TestReclamationAdmissionMirrorsRust covers the admission check in Rust
// `reclaim_pages`: incremental auto-vacuum plus at least 64 MiB and 25% free
// pages.
func TestReclamationAdmissionMirrorsRust(t *testing.T) {
	const pageSize = int64(4096)
	// 16384 free pages of 4 KiB are exactly 64 MiB on a 64 MiB database.
	if !reclamationAdmission(2, pageSize, 65536, 16384) {
		t.Fatalf("incremental auto-vacuum above both thresholds must be admitted")
	}
	cases := []struct {
		name                              string
		autoVacuum, pageSize, pages, free int64
		want                              bool
	}{
		{name: "nothing to reclaim", autoVacuum: 2, pageSize: pageSize, pages: 4096, free: 0, want: false},
		{name: "full auto-vacuum", autoVacuum: 1, pageSize: pageSize, pages: 65536, free: 16384, want: false},
		{name: "auto-vacuum off", autoVacuum: 0, pageSize: pageSize, pages: 65536, free: 16384, want: false},
		{name: "below 64 MiB free", autoVacuum: 2, pageSize: pageSize, pages: 20000, free: 6000, want: false},
		{name: "below a quarter free", autoVacuum: 2, pageSize: pageSize, pages: 262144, free: 20000, want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := reclamationAdmission(test.autoVacuum, test.pageSize, test.pages, test.free); got != test.want {
				t.Fatalf("reclamationAdmission(%d, %d, %d, %d) = %v, want %v",
					test.autoVacuum, test.pageSize, test.pages, test.free, got, test.want)
			}
		})
	}
}

// TestReclamationRetryScheduleLikeRust covers Rust `ReclamationState::retry_after`.
func TestReclamationRetryScheduleLikeRust(t *testing.T) {
	result := func(outcome reclamationOutcome, pages uint32, err error) reclamationResult {
		return reclamationResult{pass: reclamationPass{pages: pages, outcome: outcome}, err: err}
	}
	state := reclamationState{batchPages: reclamationBatchPages}
	if got := state.retryAfter(result(reclamationOutcomeIdle, 0, nil)); got != reclamationIdleInterval {
		t.Fatalf("idle retry = %v, want %v", got, reclamationIdleInterval)
	}
	if got := state.retryAfter(result(reclamationOutcomeActive, 8, nil)); got != reclamationActiveInterval {
		t.Fatalf("active retry = %v, want %v", got, reclamationActiveInterval)
	}
	if got := state.retryAfter(result(reclamationOutcomeContended, 0, nil)); got != reclamationContentionInterval {
		t.Fatalf("contended retry = %v, want %v", got, reclamationContentionInterval)
	}
	if got := state.retryAfter(result(reclamationOutcomeShutdown, 0, nil)); got != reclamationIdleInterval {
		t.Fatalf("shutdown retry = %v, want %v", got, reclamationIdleInterval)
	}
	if got := state.retryAfter(result(reclamationOutcomeInterrupted, 4, nil)); got != reclamationActiveInterval {
		t.Fatalf("interrupted pass that reclaimed pages retry = %v, want %v", got, reclamationActiveInterval)
	}
	if got := state.retryAfter(result(reclamationOutcomeIdle, 0, errors.New("boom"))); got != reclamationIdleInterval {
		t.Fatalf("failed pass retry = %v, want %v", got, reclamationIdleInterval)
	}

	// A stalled pass halves the batch (64 -> 32 -> 16 -> 8) and backs off
	// 500ms, 1s, 2s, 4s until the idle interval caps it.
	stalled := reclamationState{batchPages: reclamationBatchPages}
	wantBatch := []uint32{32, 16, 8, 4}
	wantDelay := []time.Duration{
		reclamationContentionInterval,
		2 * reclamationContentionInterval,
		4 * reclamationContentionInterval,
		8 * reclamationContentionInterval,
	}
	for i := range wantBatch {
		if got := stalled.retryAfter(result(reclamationOutcomeInterrupted, 0, nil)); got != wantDelay[i] {
			t.Fatalf("stalled retry %d = %v, want %v", i, got, wantDelay[i])
		}
		if stalled.batchPages != wantBatch[i] {
			t.Fatalf("stalled batch %d = %d, want %d", i, stalled.batchPages, wantBatch[i])
		}
	}
	// The backoff keeps doubling and is capped at the idle interval.
	wantTail := []time.Duration{
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		reclamationIdleInterval,
		reclamationIdleInterval,
	}
	for i, want := range wantTail {
		if got := stalled.retryAfter(result(reclamationOutcomeInterrupted, 0, nil)); got != want {
			t.Fatalf("stalled tail retry %d = %v, want %v", i, got, want)
		}
	}
	if stalled.batchPages != 1 {
		t.Fatalf("batch must not shrink below one page, got %d", stalled.batchPages)
	}
	// Progress keeps the smaller batch instead of growing it back.
	stalled.stalledPasses = 0
	if got := stalled.retryAfter(result(reclamationOutcomeActive, 1, nil)); got != reclamationActiveInterval {
		t.Fatalf("retry after progress = %v, want %v", got, reclamationActiveInterval)
	}
	if stalled.batchPages != 1 {
		t.Fatalf("progress must keep the smaller batch, got %d", stalled.batchPages)
	}
}

// TestReclamationOwnershipElectsOneWorkerLikeRust covers Rust `try_ownership`:
// one owner per home, no error when another worker holds the lock.
func TestReclamationOwnershipElectsOneWorkerLikeRust(t *testing.T) {
	home := t.TempDir()
	first, err := tryReclamationOwnership(home)
	if err != nil {
		t.Fatalf("first tryReclamationOwnership: %v", err)
	}
	if first == nil {
		t.Fatalf("first worker must own the home")
	}
	second, err := tryReclamationOwnership(home)
	if err != nil {
		t.Fatalf("second tryReclamationOwnership: %v", err)
	}
	if second != nil {
		t.Fatalf("a held lock must elect no second owner")
	}
	if _, err := filepath.Abs(filepath.Join(home, reclamationLockFile)); err != nil {
		t.Fatal(err)
	}
	if err := first.Unlock(); err != nil {
		t.Fatalf("release: %v", err)
	}
	third, err := tryReclamationOwnership(home)
	if err != nil {
		t.Fatalf("third tryReclamationOwnership: %v", err)
	}
	if third == nil {
		t.Fatalf("the home must be acquirable after the owner releases it")
	}
	_ = third.Unlock()
}

// TestReclamationWorkerDefersToTheHomeOwnerLikeRust covers the spawn loop: the
// elected worker visits its databases while a second worker for the same home
// waits for the lock, without touching a database.
func TestReclamationWorkerDefersToTheHomeOwnerLikeRust(t *testing.T) {
	config := mustSqliteConfig(t, t.TempDir())
	var firstPasses, secondPasses atomic.Int64
	newWorker := func(counter *atomic.Int64) *sqliteReclamationWorker {
		worker := newSqliteReclamationWorker(config)
		if worker == nil {
			t.Fatalf("the logs database must opt into background reclamation")
		}
		if len(worker.targets) != 1 || worker.targets[0].path != config.LogsDBPath() {
			t.Fatalf("reclamation targets = %#v, want only the logs database", worker.targets)
		}
		worker.idleInterval = time.Millisecond
		worker.probe = func(context.Context, reclamationTarget, reclamationOptions, <-chan struct{}) reclamationResult {
			counter.Add(1)
			return reclamationResult{pass: reclamationPass{outcome: reclamationOutcomeActive}}
		}
		return worker
	}

	first := newWorker(&firstPasses)
	go first.run()
	t.Cleanup(first.close)
	waitForReclamation(t, func() bool { return firstPasses.Load() > 0 }, "the owner to run a pass")

	second := newWorker(&secondPasses)
	go second.run()
	t.Cleanup(second.close)
	time.Sleep(50 * time.Millisecond)
	if got := secondPasses.Load(); got != 0 {
		t.Fatalf("a second worker for the same home ran %d passes, want 0 while the lock is held", got)
	}

	first.close()
	waitForReclamation(t, func() bool { return secondPasses.Load() > 0 }, "the second worker to take over after the owner released the lock")
}

// TestReclamationWorkerCloseWaitsForThePassLikeRust covers Rust `close`: it
// signals shutdown and waits for the task, so a pass in flight finishes (and
// releases its dedicated connection) before the pools close.
func TestReclamationWorkerCloseWaitsForThePassLikeRust(t *testing.T) {
	config := mustSqliteConfig(t, t.TempDir())
	worker := newSqliteReclamationWorker(config)
	if worker == nil {
		t.Fatalf("the logs database must opt into background reclamation")
	}
	worker.idleInterval = time.Millisecond
	started := make(chan struct{})
	release := make(chan struct{})
	worker.probe = func(context.Context, reclamationTarget, reclamationOptions, <-chan struct{}) reclamationResult {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return reclamationResult{pass: reclamationPass{outcome: reclamationOutcomeActive}}
	}
	go worker.run()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("the worker never started a pass")
	}

	closed := make(chan struct{})
	go func() {
		worker.close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatalf("close returned while a reclamation pass was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatalf("close did not return after the in-flight pass finished")
	}
	select {
	case <-worker.finished:
	default:
		t.Fatalf("the worker goroutine must have exited before close returns")
	}
}

// TestInitStateRuntimeStartsReclamationLikeRust covers the wiring: the runtime
// constructs the worker for the databases that opted in and stops it in Close.
func TestInitStateRuntimeStartsReclamationLikeRust(t *testing.T) {
	ctx := context.Background()
	config := mustSqliteConfig(t, t.TempDir())
	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime: %v", err)
	}
	if runtime.reclamation == nil {
		_ = runtime.Close()
		t.Fatalf("InitStateRuntime must spawn the reclamation worker")
	}
	targets := runtime.reclamation.targets
	if len(targets) != 1 || targets[0].label != "log DB" || targets[0].path != config.LogsDBPath() {
		_ = runtime.Close()
		t.Fatalf("reclamation targets = %#v, want only the logs database", targets)
	}
	worker := runtime.reclamation
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-worker.finished:
	default:
		t.Fatalf("Close must stop the reclamation worker")
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestRuntimeDBPathsOnlyOptsLogsIntoReclamationLikeRust covers the per-spec
// flag Rust exposes on `RuntimeDbPath`.
func TestRuntimeDBPathsOnlyOptsLogsIntoReclamationLikeRust(t *testing.T) {
	config := mustSqliteConfig(t, t.TempDir())
	opted := []string{}
	for _, db := range config.RuntimeDBPaths() {
		if db.BackgroundReclamation {
			opted = append(opted, db.Path)
		}
	}
	if len(opted) != 1 || opted[0] != config.LogsDBPath() {
		t.Fatalf("databases opting into reclamation = %#v, want only %s", opted, config.LogsDBPath())
	}
}

func waitForReclamation(t *testing.T, done func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// reclamationLogsDB seeds a migrated logs database whose free pages exceed the
// 64 MiB admission threshold, mirroring the fixture in Rust
// `interrupted_reclamation_releases_writer_and_resumes_without_data_loss`
// (Rust #49069, upstream 33a0f766a6).
func reclamationLogsDB(t *testing.T, rows int64) (SqliteConfig, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	config := mustSqliteConfig(t, t.TempDir())
	db, err := config.OpenReadWrite(ctx, config.LogsDBPath())
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrateRuntimeDB(ctx, db, RuntimeDBLogs); err != nil {
		t.Fatalf("migrate logs database: %v", err)
	}
	seed := fmt.Sprintf(`WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i < %d) `+
		`INSERT INTO logs (ts, ts_nanos, level, target, feedback_log_body, thread_id, process_uuid) `+
		`SELECT unixepoch(), 0, 'INFO', 'fixture', printf('%%032768d', i), 'thread-'||i, 'process-'||(i%%17) FROM n`, rows)
	if _, err := db.ExecContext(ctx, seed); err != nil {
		t.Fatalf("seed logs: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM logs WHERE id%4 != 0`); err != nil {
		t.Fatalf("delete logs: %v", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint logs: %v", err)
	}
	return config, db
}

func reclamationMaintenanceConn(t *testing.T, path string) *sql.Conn {
	t.Helper()
	ctx := context.Background()
	db, err := openReclamationDB(ctx, path)
	if err != nil {
		t.Fatalf("openReclamationDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("maintenance Conn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func reclamationQueryInt(t *testing.T, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query string) int64 {
	t.Helper()
	var value int64
	if err := q.QueryRowContext(context.Background(), query).Scan(&value); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return value
}

// TestReclamationPassPreservesTheReserveLikeRust covers Rust `reclaim_pages`:
// one pass reclaims until only the reserve is free, reports exactly the pages it
// removed and leaves the retained rows and the file intact.
func TestReclamationPassPreservesTheReserveLikeRust(t *testing.T) {
	ctx := context.Background()
	config, db := reclamationLogsDB(t, 4096)
	pageSize := reclamationQueryInt(t, db, `PRAGMA page_size`)
	pagesBefore := reclamationQueryInt(t, db, `PRAGMA page_count`)
	freeBefore := reclamationQueryInt(t, db, `PRAGMA freelist_count`)
	bytesBefore := int64(len(mustReadFile(t, config.LogsDBPath())))
	conn := reclamationMaintenanceConn(t, config.LogsDBPath())

	pass, err := reclaimPages(ctx, conn, reclamationOptions{
		budget:     reclamationBudget{deadline: time.Now().Add(2 * time.Minute), pages: 1 << 20},
		batchPages: 1024,
	}, nil)
	if err != nil {
		t.Fatalf("reclaimPages: %v", err)
	}
	if pass.outcome != reclamationOutcomeIdle {
		t.Fatalf("outcome = %v, pages = %d, free = %d, reserve = %d", pass.outcome, pass.pages,
			reclamationQueryInt(t, db, `PRAGMA freelist_count`), reclamationReservePages(pageSize, pagesBefore))
	}
	wantReserve := reclamationReservePages(pageSize, pagesBefore)
	free := reclamationQueryInt(t, db, `PRAGMA freelist_count`)
	if free != wantReserve {
		t.Fatalf("free pages after the pass = %d, want the %d-page reserve", free, wantReserve)
	}
	if free*pageSize < reclamationReserveBytes {
		t.Fatalf("reserve = %d bytes, want at least %d", free*pageSize, reclamationReserveBytes)
	}
	// Rust asserts the reported pages equal the freelist delta, and that the
	// database shrinks by at least that many pages.
	if got, want := freeBefore-free, int64(pass.pages); got != want {
		t.Fatalf("freelist shrank by %d pages but the pass reported %d", got, want)
	}
	if pass.pages == 0 {
		t.Fatalf("pass reclaimed nothing from a database with a large freelist")
	}
	pagesAfter := reclamationQueryInt(t, db, `PRAGMA page_count`)
	if pagesAfter > pagesBefore-int64(pass.pages) {
		t.Fatalf("database went from %d to %d pages after reclaiming %d", pagesBefore, pagesAfter, pass.pages)
	}
	bytesAfter := int64(len(mustReadFile(t, config.LogsDBPath())))
	if bytesAfter >= bytesBefore {
		t.Fatalf("reclamation must shrink the database: %d -> %d bytes", bytesBefore, bytesAfter)
	}
	// The retained rows are untouched and the database stays healthy.
	var rows, longest int64
	if err := db.QueryRowContext(ctx, `SELECT count(*), max(length(feedback_log_body)) FROM logs`).Scan(&rows, &longest); err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if rows != 1024 || longest != 32768 {
		t.Fatalf("retained rows changed: count=%d longest=%d", rows, longest)
	}
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q, want ok", integrity)
	}
}

// TestReclamationWorkerReclaimsLogsPagesLikeRust covers the wiring end to end:
// the runtime's worker owns the home and shrinks the logs database without any
// test-visible hook.
func TestReclamationWorkerReclaimsLogsPagesLikeRust(t *testing.T) {
	config, db := reclamationLogsDB(t, 4096)
	freeBefore := reclamationQueryInt(t, db, `PRAGMA freelist_count`)
	worker := newSqliteReclamationWorker(config)
	if worker == nil {
		t.Fatalf("the logs database must opt into background reclamation")
	}
	// Only the ownership election is sped up; the pass keeps Rust's budget.
	worker.idleInterval = time.Millisecond
	go worker.run()
	defer worker.close()
	waitForReclamation(t, func() bool {
		return reclamationQueryInt(t, db, `PRAGMA freelist_count`) < freeBefore
	}, "the worker to reclaim log database pages")
}

// TestReclamationPassDefersWhileAnotherWriterHoldsTheLockLikeRust covers Rust's
// contention path: a second writer defers the pass instead of queueing behind
// it, because the maintenance connection uses no busy wait.
func TestReclamationPassDefersWhileAnotherWriterHoldsTheLockLikeRust(t *testing.T) {
	ctx := context.Background()
	config, db := reclamationLogsDB(t, 4096)
	writer, err := config.OpenReadWrite(ctx, config.LogsDBPath())
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	defer func() { _ = writer.Close() }()
	writerConn, err := writer.Conn(ctx)
	if err != nil {
		t.Fatalf("writer Conn: %v", err)
	}
	defer func() { _ = writerConn.Close() }()
	if _, err := writerConn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("writer BEGIN IMMEDIATE: %v", err)
	}
	defer func() { _, _ = writerConn.ExecContext(context.Background(), `ROLLBACK`) }()

	freeBefore := reclamationQueryInt(t, db, `PRAGMA freelist_count`)
	conn := reclamationMaintenanceConn(t, config.LogsDBPath())
	pass, err := reclaimPages(ctx, conn, reclamationOptions{
		budget:     reclamationBudget{deadline: time.Now().Add(30 * time.Second), pages: 1024},
		batchPages: 64,
	}, nil)
	if err != nil {
		t.Fatalf("reclaimPages must defer instead of failing: %v", err)
	}
	if pass.outcome != reclamationOutcomeContended {
		t.Fatalf("outcome = %v, want contended while another writer holds the lock", pass.outcome)
	}
	if pass.pages != 0 {
		t.Fatalf("pages = %d, want 0 while another writer holds the lock", pass.pages)
	}
	if free := reclamationQueryInt(t, db, `PRAGMA freelist_count`); free != freeBefore {
		t.Fatalf("freelist changed from %d to %d while another writer held the lock", freeBefore, free)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return content
}
