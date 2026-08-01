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
	message := "legacy rendered body"
	warnBody := "operation alpha failed"
	infoBody := "operation beta complete"
	thread1, thread2 := "thread-1", "thread-2"
	process := "process-1"
	moduleA, moduleB := "codex.alpha", "codex.beta"
	fileA, fileB := "alpha.go", "beta.go"
	line7, line8 := int64(7), int64(8)
	entries := []LogEntry{
		{TS: 10, TSNanos: 1, Level: "debug", Target: "legacy", Message: &message, ThreadID: &thread1},
		{TS: 20, TSNanos: 2, Level: "WARN", Target: "worker", FeedbackLogBody: &warnBody, ThreadID: &thread1, ProcessUUID: &process, ModulePath: &moduleA, File: &fileA, Line: &line7},
		{TS: 30, TSNanos: 3, Level: "INFO", Target: "worker", FeedbackLogBody: &infoBody, ThreadID: &thread2, ProcessUUID: &process, ModulePath: &moduleB, File: &fileB, Line: &line8},
		{TS: 40, TSNanos: 4, Level: "ERROR", Target: "process", FeedbackLogBody: stringPointer("threadless"), ProcessUUID: &process},
	}
	if err := runtime.InsertLogs(ctx, entries); err != nil {
		t.Fatal(err)
	}

	all, err := runtime.QueryLogs(ctx, LogQuery{})
	if err != nil || len(all) != 4 || all[0].Message == nil || *all[0].Message != message {
		t.Fatalf("all logs = %#v, %v", all, err)
	}
	from, to, after, limit := int64(15), int64(35), int64(1), int64(1)
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
	thread, otherThread := "busy-thread", "other-thread"
	processA, processB := "process-a", "process-b"
	entries := make([]LogEntry, 0, 2_005)
	for ts := int64(1); ts <= 1_001; ts++ {
		entries = append(entries, LogEntry{TS: ts, Level: "INFO", Target: "test", ThreadID: &thread})
		entries = append(entries, LogEntry{TS: ts, Level: "INFO", Target: "test", ProcessUUID: &processA})
	}
	entries = append(entries,
		LogEntry{TS: 1, Level: "INFO", Target: "test", ThreadID: &otherThread},
		LogEntry{TS: 1, Level: "INFO", Target: "test", ProcessUUID: &processB},
	)
	if err := runtime.InsertLogs(ctx, entries); err != nil {
		t.Fatal(err)
	}
	threadRows, err := runtime.QueryLogs(ctx, LogQuery{ThreadIDs: []string{thread}, Descending: true})
	if err != nil || len(threadRows) != 1_000 || threadRows[0].TS != 1_001 || threadRows[len(threadRows)-1].TS != 2 {
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
		{TS: 1, Level: "INFO", Target: "test", FeedbackLogBody: &sixMiB, ThreadID: &byteThread},
		{TS: 2, Level: "INFO", Target: "test", FeedbackLogBody: &sixMiB, ThreadID: &byteThread},
	}); err != nil {
		t.Fatal(err)
	}
	byteRows, err := runtime.QueryLogs(ctx, LogQuery{ThreadIDs: []string{byteThread}})
	if err != nil || len(byteRows) != 1 || byteRows[0].TS != 2 {
		t.Fatalf("byte-pruned logs len=%d first=%d err=%v", len(byteRows), firstLogTS(byteRows), err)
	}
}

func TestQueryFeedbackLogsUsesLatestThreadProcessesAndChronologicalOutput(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	ctx := context.Background()
	thread1, thread2 := "thread-1", "thread-2"
	oldProcess, newProcess, secondProcess, unrelated := "old", "new", "second", "unrelated"
	entries := []LogEntry{
		feedbackEntry(1, 123_456_000, "INFO", "old thread", &thread1, &oldProcess),
		feedbackEntry(2, 0, "WARN", "new thread", &thread1, &newProcess),
		feedbackEntry(3, 0, "INFO", "second thread", &thread2, &secondProcess),
		feedbackEntry(4, 0, "DEBUG", "old process global", nil, &oldProcess),
		feedbackEntry(5, 0, "ERROR", "new process global", nil, &newProcess),
		feedbackEntry(6, 0, "INFO", "second process global\n", nil, &secondProcess),
		feedbackEntry(7, 0, "INFO", "unrelated global", nil, &unrelated),
	}
	if err := runtime.InsertLogs(ctx, entries); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.QueryFeedbackLogsForThreads(ctx, []string{thread1, thread2, thread1})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"1970-01-01T00:00:01.123456Z  INFO old thread\n",
		"1970-01-01T00:00:02.000000Z  WARN new thread\n",
		"1970-01-01T00:00:03.000000Z  INFO second thread\n",
		"1970-01-01T00:00:05.000000Z ERROR new process global\n",
		"1970-01-01T00:00:06.000000Z  INFO second process global\n",
	}, "")
	if string(got) != want {
		t.Fatalf("feedback logs:\n%s\nwant:\n%s", got, want)
	}
	empty, err := runtime.QueryFeedbackLogsForThreads(ctx, nil)
	if err != nil || !reflect.DeepEqual(empty, []byte{}) {
		t.Fatalf("empty feedback = %#v, %v", empty, err)
	}
}

func TestLogsStartupMaintenanceRetainsTenDays(t *testing.T) {
	home := t.TempDir()
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(context.Background(), config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	oldBody, recentBody := "old", "recent"
	if err := runtime.InsertLogs(context.Background(), []LogEntry{
		{TS: now.Add(-11 * 24 * time.Hour).Unix(), Level: "INFO", Target: "test", FeedbackLogBody: &oldBody},
		{TS: now.Add(-9 * 24 * time.Hour).Unix(), Level: "INFO", Target: "test", FeedbackLogBody: &recentBody},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := InitStateRuntime(context.Background(), config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err := reopened.QueryLogs(context.Background(), LogQuery{})
	if err != nil || len(rows) != 1 || rows[0].Message == nil || *rows[0].Message != recentBody {
		t.Fatalf("retained logs = %#v, %v", rows, err)
	}
}

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
