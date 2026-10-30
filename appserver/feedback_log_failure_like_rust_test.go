package appserver

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/state"
)

// Mirrors Rust #47886: when SQLite lost a log batch, feedback/upload discards
// the incomplete database selection, attaches the memory ring instead, keeps
// the captured tags and thread identity, and never exposes the raw SQLite error.
func TestFeedbackUploadFallsBackToMemoryLogsWhenSQLiteWriteFailsLikeRust(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(ctx, sqliteConfig, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()
	store := session.NewStore(home)
	threadRouter := NewRouter(store)
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	record := &session.Record{ID: "thread-feedback-ring", SessionID: "thread-feedback-ring", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{CWD: home, ModelProvider: "openai"}}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := threadRouter.createThreadRollout(record, record.CreatedAt); err != nil {
		t.Fatal(err)
	}
	logHandler := state.NewLogDBHandlerWithConfig(stateRuntime, nil, state.LogSinkQueueConfig{QueueCapacity: 8, BatchSize: 1, FlushInterval: time.Hour})
	defer func() { _ = logHandler.Close(ctx) }()
	logger := slog.New(logHandler)
	logger.Info("persisted log", "target", "test", "thread_id", "thread-feedback-ring")
	if err := logHandler.Flush(ctx); err != nil {
		t.Fatalf("flush persisted log: %v", err)
	}
	if _, err := stateRuntime.LogsDB().ExecContext(ctx,
		"CREATE TRIGGER fail_log_insert BEFORE INSERT ON logs BEGIN SELECT RAISE(ABORT, 'synthetic-secret'); END",
	); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}
	logger.Info("lost memory log", "target", "test", "thread_id", "thread-feedback-ring")

	snapshot := &FeedbackSnapshot{
		Tags:        map[string]string{"model": "gpt-5"},
		Diagnostics: NewFeedbackDiagnostics(nil),
	}
	diagnostics := snapshot.Diagnostics
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		StateRuntime: stateRuntime,
		LogDBHandler: logHandler,
		Feedback:     snapshot,
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodFeedbackUpload, FeedbackUploadParams{
		Classification: "bug",
		ThreadID:       stringPointerAppserver("thread-feedback-ring"),
		IncludeLogs:    true,
		Tags:           map[string]string{"model": "gpt-5"},
	}))
	if response.Error != nil {
		t.Fatalf("feedback upload error = %+v", response.Error)
	}
	prepared := snapshot.LastPrepared
	if prepared == nil {
		t.Fatal("feedback upload was not prepared")
	}
	if snapshot.ThreadID != "thread-feedback-ring" || prepared.Tags["thread_id"] != "thread-feedback-ring" || prepared.Tags["model"] != "gpt-5" {
		t.Fatalf("snapshot identity = %q tags=%#v", snapshot.ThreadID, prepared.Tags)
	}
	if snapshot.Diagnostics != diagnostics {
		t.Fatal("refreshing logs replaced the captured diagnostics")
	}
	logs := feedbackAttachmentByName(prepared.Attachments, "codex-logs.log")
	if logs == nil {
		t.Fatalf("feedback attachments = %#v", prepared.Attachments)
	}
	content := string(logs.Buffer)
	for _, want := range []string{"persisted log", "lost memory log", `failed to flush logs to SQLite error="constraint" entries=1`} {
		if !strings.Contains(content, want) {
			t.Fatalf("feedback logs %q missing %q", content, want)
		}
	}
	if strings.Contains(content, "synthetic-secret") {
		t.Fatalf("feedback logs leaked raw SQLite error details: %q", content)
	}
}
