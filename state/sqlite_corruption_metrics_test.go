package state

import (
	"context"
	"reflect"
	"testing"
)

// Mirrors the telemetry assertions of Rust #49701 (upstream `3620b2caf8`,
// `codex-rs/state/src/sqlite.rs::record_corruption`): a confirmed
// `PRAGMA quick_check(1)` finding is recorded once per database through
// `codex.sqlite.corruption.count{db=…}` — including a report-only database that
// is never rebuilt — and the tag is the database kind.
func TestDBCorruptionMetricsLikeRust(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	metrics := NewTaskMetrics()
	config = config.WithCorruptionMetrics(metrics)

	corruptValidationDB(t, config.StateDBPath())
	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime on corrupt state db: %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}

	// The rebuilt state database is healthy, so reopening records nothing new;
	// thread history is opened lazily and reports its own finding without being
	// rebuilt.
	corruptValidationDB(t, config.ThreadHistoryDBPath())
	runtime, err = InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatalf("second InitStateRuntime: %v", err)
	}
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatalf("ThreadHistoryDB: %v", err)
	}
	if got := quickCheck(ctx, db, testQuickCheckBudget); got != QuickCheckCorruptedNeedsFixed {
		t.Fatalf("thread history quick_check = %v, want %v", got, QuickCheckCorruptedNeedsFixed)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close second runtime: %v", err)
	}

	got := corruptionCounts(metrics)
	want := map[string]int{"state": 1, "thread_history": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("corruption counts = %v, want %v (records: %v)", got, want, metrics.Records())
	}
}

// Mirrors Rust `record_corruption`'s "no sink installed" path: findings are
// counted per kind and a nil sink records nothing.
func TestDBCorruptionMetricsAreOptionalLikeRust(t *testing.T) {
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatalf("NewSqliteConfig: %v", err)
	}
	metrics := NewTaskMetrics()
	corruptValidationDB(t, config.StateDBPath())
	// No sink installed: openRuntimeDB still recovers the database.
	runtime, err := InitStateRuntime(context.Background(), config, "openai")
	if err != nil {
		t.Fatalf("InitStateRuntime without metrics: %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	if records := metrics.Records(); len(records) != 0 {
		t.Fatalf("unrelated metrics sink recorded %#v", records)
	}
	recordDBCorruption(nil, RuntimeDBState)
	if records := metrics.Records(); len(records) != 0 {
		t.Fatalf("nil sink recorded %#v", records)
	}
}

func corruptionCounts(metrics *TaskMetrics) map[string]int {
	counts := map[string]int{}
	for _, record := range metrics.Records() {
		if record.Name != DBCorruptionMetric {
			continue
		}
		counts[record.Tags["db"]] += record.Inc
	}
	return counts
}
