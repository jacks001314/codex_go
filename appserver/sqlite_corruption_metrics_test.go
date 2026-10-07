package appserver

import (
	"context"
	"testing"

	"codex_go/session"
	"codex_go/state"
)

// Mirrors Rust #49701 (upstream `3620b2caf8`, `codex-rs/app-server/src/lib.rs`
// passing a `telemetry_override` into the state opener): the app server records
// startup SQLite corruption into the sink it chose before the runtime was
// opened, and every later metric on that router reuses the same instance.
func TestRuntimeRouterReusesStartupCorruptionMetricsLikeRust(t *testing.T) {
	home := t.TempDir()
	writeCorruptStateDBFixture(t, home)

	metrics := state.NewTaskMetrics()
	prepared, owned, err := prepareSharedStateRuntime(context.Background(), home, &RuntimeRouterOptions{
		DBCorruptionMetrics: metrics,
	})
	if err != nil {
		t.Fatalf("prepareSharedStateRuntime: %v", err)
	}
	if owned == nil {
		t.Fatal("expected the shared startup path to own a state runtime")
	}
	defer owned.Close()

	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, prepared)
	defer router.Close()
	if router.taskMetrics() != metrics {
		t.Fatalf("router metrics = %p, want the injected sink %p", router.taskMetrics(), metrics)
	}
	counts := map[string]int{}
	for _, record := range metrics.Records() {
		if record.Name == state.DBCorruptionMetric {
			counts[record.Tags["db"]] += record.Inc
		}
	}
	if len(counts) != 1 || counts["state"] != 1 {
		t.Fatalf("startup corruption metrics = %v, want map[state:1] (records: %v)", counts, metrics.Records())
	}
}

// Mirrors Rust #49701: the sink is optional, and a router built without one
// still recovers damaged databases.
func TestRuntimeRouterRecoversWithoutMetricsLikeRust(t *testing.T) {
	home := t.TempDir()
	writeCorruptStateDBFixture(t, home)

	prepared, owned, err := prepareSharedStateRuntime(context.Background(), home, nil)
	if err != nil {
		t.Fatalf("prepareSharedStateRuntime without metrics: %v", err)
	}
	if owned == nil {
		t.Fatal("expected the shared startup path to own a state runtime")
	}
	defer owned.Close()
	if prepared.DBCorruptionMetrics == nil {
		t.Fatal("the shared startup path must still install a metrics sink")
	}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, prepared)
	defer router.Close()
	if router.taskMetrics() == nil {
		t.Fatal("router metrics sink is nil")
	}
}

// writeCorruptStateDBFixture builds the upstream corruption fixture: the
// database opens (and migrates) while `PRAGMA quick_check(1)` reports
// `NULL value in sample.value` (Rust #49701 validation_tests.rs).
func writeCorruptStateDBFixture(t *testing.T, home string) {
	t.Helper()
	ctx := context.Background()
	config, err := state.SqliteConfigForCodexHome(home)
	if err != nil {
		t.Fatalf("SqliteConfigForCodexHome: %v", err)
	}
	path := config.StateDBPath()
	db, err := state.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	statements := []string{
		"CREATE TABLE sample(value INTEGER)",
		"INSERT INTO sample VALUES (NULL)",
		"PRAGMA writable_schema=ON",
		"UPDATE sqlite_schema SET sql='CREATE TABLE sample(value INTEGER NOT NULL)' WHERE name='sample'",
		"PRAGMA writable_schema=OFF",
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

// writeCorruptRuntimeDB applies the upstream corruption fixture to an existing
// runtime database: it opens (and migrates) while `PRAGMA quick_check(1)`
// reports `NULL value in sample.value` (Rust #49701 validation_tests.rs).
func writeCorruptRuntimeDB(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	db, err := state.OpenSQLite(ctx, path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer db.Close()
	statements := []string{
		"CREATE TABLE sample(value INTEGER)",
		"INSERT INTO sample VALUES (NULL)",
		"PRAGMA writable_schema=ON",
		"UPDATE sqlite_schema SET sql='CREATE TABLE sample(value INTEGER NOT NULL)' WHERE name='sample'",
		"PRAGMA writable_schema=OFF",
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s on %s: %v", statement, path, err)
		}
	}
}
