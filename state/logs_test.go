package state

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLogsInsertQueryFiltersAndFallback(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	ctx := context.Background()
	// Rust #49425 (upstream 8ea2c0e0d4): the diagnostic-log sweep now runs in a
	// background task that fires immediately on initialization, so fixtures must
	// use timestamps inside the retention window. Upstream updated the mirrored
	// tests to `Utc::now()`-relative values for the same reason.
	now := time.Now().Unix()
	message := "legacy rendered body"
	warnBody := "operation alpha failed"
	infoBody := "operation beta complete"
	thread1, thread2 := "thread-1", "thread-2"
	process := "process-1"
	moduleA, moduleB := "codex.alpha", "codex.beta"
	fileA, fileB := "alpha.go", "beta.go"
	line7, line8 := int64(7), int64(8)
	entries := []LogEntry{
		{TS: now + 10, TSNanos: 1, Level: "debug", Target: "legacy", Message: &message, ThreadID: &thread1},
		{TS: now + 20, TSNanos: 2, Level: "WARN", Target: "worker", FeedbackLogBody: &warnBody, ThreadID: &thread1, ProcessUUID: &process, ModulePath: &moduleA, File: &fileA, Line: &line7},
		{TS: now + 30, TSNanos: 3, Level: "INFO", Target: "worker", FeedbackLogBody: &infoBody, ThreadID: &thread2, ProcessUUID: &process, ModulePath: &moduleB, File: &fileB, Line: &line8},
		{TS: now + 40, TSNanos: 4, Level: "ERROR", Target: "process", FeedbackLogBody: stringPointer("threadless"), ProcessUUID: &process},
	}
	if err := runtime.InsertLogs(ctx, entries); err != nil {
		t.Fatal(err)
	}

	all, err := runtime.QueryLogs(ctx, LogQuery{})
	if err != nil || len(all) != 4 || all[0].Message == nil || *all[0].Message != message {
		t.Fatalf("all logs = %#v, %v", all, err)
	}
	from, to, after, limit := now+15, now+35, int64(1), int64(1)
	search := "alpha"
	filtered, err := runtime.QueryLogs(ctx, LogQuery{
		LevelsUpper: []string{"WARN", "ERROR"}, FromTS: &from, ToTS: &to,
		ModuleLike: []string{"alpha"}, FileLike: []string{"pha.go"},
		ThreadIDs: []string{thread1}, IncludeThreadless: true,
		Search: &search, AfterID: &after, Limit: &limit, Descending: true,
	})
	if err != nil || len(filtered) != 1 || filtered[0].ID != 2 || filtered[0].Level != "WARN" || filtered[0].Line == nil || *filtered[0].Line != line7 {
		t.Fatalf("filtered logs = %#v, %v", filtered, err)
	}
	maxID, err := runtime.MaxLogID(ctx, LogQuery{ThreadIDs: []string{thread1}})
	if err != nil || maxID != 2 {
		t.Fatalf("max thread log id = %d, %v", maxID, err)
	}
	maxID, err = runtime.MaxLogID(ctx, LogQuery{Search: stringPointer("missing")})
	if err != nil || maxID != 0 {
		t.Fatalf("empty max log id = %d, %v", maxID, err)
	}
}

func TestLogsPruneEachRustPartitionByRowAndByteLimit(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	ctx := context.Background()
	// Rust #49425 (upstream 8ea2c0e0d4): background retention prunes by age, so
	// every fixture timestamp is relative to now (upstream `now + ts`).
	now := time.Now().Unix()
	thread, otherThread := "busy-thread", "other-thread"
	processA, processB := "process-a", "process-b"
	entries := make([]LogEntry, 0, 2_005)
	for ts := int64(1); ts <= 1_001; ts++ {
		entries = append(entries, LogEntry{TS: now + ts, Level: "INFO", Target: "test", ThreadID: &thread})
		entries = append(entries, LogEntry{TS: now + ts, Level: "INFO", Target: "test", ProcessUUID: &processA})
	}
	entries = append(entries,
		LogEntry{TS: now + 1, Level: "INFO", Target: "test", ThreadID: &otherThread},
		LogEntry{TS: now + 1, Level: "INFO", Target: "test", ProcessUUID: &processB},
	)
	if err := runtime.InsertLogs(ctx, entries); err != nil {
		t.Fatal(err)
	}
	threadRows, err := runtime.QueryLogs(ctx, LogQuery{ThreadIDs: []string{thread}, Descending: true})
	if err != nil || len(threadRows) != 1_000 || threadRows[0].TS != now+1_001 || threadRows[len(threadRows)-1].TS != now+2 {
		t.Fatalf("row-pruned thread logs len=%d first/last=%d/%d err=%v", len(threadRows), firstLogTS(threadRows), lastLogTS(threadRows), err)
	}
	threadless, err := runtime.QueryLogs(ctx, LogQuery{IncludeThreadless: true})
	if err != nil || len(threadless) != 1_001 {
		t.Fatalf("threadless partitions len=%d err=%v", len(threadless), err)
	}
	other, err := runtime.QueryLogs(ctx, LogQuery{ThreadIDs: []string{otherThread}})
	if err != nil || len(other) != 1 {
		t.Fatalf("other thread partition = %#v, %v", other, err)
	}

	sixMiB := strings.Repeat("x", 6*1024*1024)
	byteThread := "byte-thread"
	if err := runtime.InsertLogs(ctx, []LogEntry{
		{TS: now + 1, Level: "INFO", Target: "test", FeedbackLogBody: &sixMiB, ThreadID: &byteThread},
		{TS: now + 2, Level: "INFO", Target: "test", FeedbackLogBody: &sixMiB, ThreadID: &byteThread},
	}); err != nil {
		t.Fatal(err)
	}
	byteRows, err := runtime.QueryLogs(ctx, LogQuery{ThreadIDs: []string{byteThread}})
	if err != nil || len(byteRows) != 1 || byteRows[0].TS != now+2 {
		t.Fatalf("byte-pruned logs len=%d first=%d err=%v", len(byteRows), firstLogTS(byteRows), err)
	}
}

func TestQueryFeedbackLogsUsesLatestThreadProcessesAndChronologicalOutput(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	ctx := context.Background()
	// Rust #49425 (upstream 8ea2c0e0d4): the bug-report feedback fixture is now
	// anchored to the current time so background retention keeps it (upstream
	// formats the expected lines from `now + n` too).
	now := time.Now().Unix()
	thread1, thread2 := "thread-1", "thread-2"
	oldProcess, newProcess, secondProcess, unrelated := "old", "new", "second", "unrelated"
	entries := []LogEntry{
		feedbackEntry(now+1, 123_456_000, "INFO", "old thread", &thread1, &oldProcess),
		feedbackEntry(now+2, 0, "WARN", "new thread", &thread1, &newProcess),
		feedbackEntry(now+3, 0, "INFO", "second thread", &thread2, &secondProcess),
		feedbackEntry(now+4, 0, "DEBUG", "old process global", nil, &oldProcess),
		feedbackEntry(now+5, 0, "ERROR", "new process global", nil, &newProcess),
		feedbackEntry(now+6, 0, "INFO", "second process global\n", nil, &secondProcess),
		feedbackEntry(now+7, 0, "INFO", "unrelated global", nil, &unrelated),
	}
	if err := runtime.InsertLogs(ctx, entries); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.QueryFeedbackLogsForThreads(ctx, []string{thread1, thread2, thread1})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		formatFeedbackLogLine(now+1, 123_456_000, "INFO", "old thread"),
		formatFeedbackLogLine(now+2, 0, "WARN", "new thread"),
		formatFeedbackLogLine(now+3, 0, "INFO", "second thread"),
		formatFeedbackLogLine(now+5, 0, "ERROR", "new process global"),
		formatFeedbackLogLine(now+6, 0, "INFO", "second process global\n"),
	}, "")
	if string(got) != want {
		t.Fatalf("feedback logs:\n%s\nwant:\n%s", got, want)
	}
	empty, err := runtime.QueryFeedbackLogsForThreads(ctx, nil)
	if err != nil || !reflect.DeepEqual(empty, []byte{}) {
		t.Fatalf("empty feedback = %#v, %v", empty, err)
	}
}

// TestLogsStartupMaintenanceRetainsTenDays covered the startup-only sweep that
// Rust #49425 (upstream 8ea2c0e0d4) removed: `run_logs_startup_maintenance` no
// longer exists, so the checks live in logs_maintenance_test.go instead
// (TestPruneLogsByAgeAndSizeKeepsTenDayBoundaryLikeRust for the 10-day window,
// TestStartupLogsCleanupRunsWithoutWaitingForThePeriodLikeRust for the sweep
// that now happens immediately after initialization).

func feedbackEntry(ts int64, nanos int64, level string, body string, threadID *string, processUUID *string) LogEntry {
	return LogEntry{TS: ts, TSNanos: nanos, Level: level, Target: "test", FeedbackLogBody: &body, ThreadID: threadID, ProcessUUID: processUUID}
}

func firstLogTS(rows []LogRow) int64 {
	if len(rows) == 0 {
		return 0
	}
	return rows[0].TS
}

func lastLogTS(rows []LogRow) int64 {
	if len(rows) == 0 {
		return 0
	}
	return rows[len(rows)-1].TS
}

func TestInsertLogsEmitsWriteTelemetry(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	metrics := NewTaskMetrics()
	runtime.SetMetrics(metrics)
	message := "telemetry probe"
	now := time.Now().Unix()
	if err := runtime.InsertLogs(context.Background(), []LogEntry{{TS: now, TSNanos: 1, Level: "INFO", Target: "worker", Message: &message}}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rec := range metrics.Records() {
		if rec.Name == "codex.sqlite.log.write" && rec.Kind == "counter" && rec.Tags["status"] == "ok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no codex.sqlite.log.write metric: %#v", metrics.Records())
	}
}
