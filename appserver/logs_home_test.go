package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/session"
	"codex_go/state"
)

// Rust #55978 routes logs_2.sqlite into an explicitly configured log_dir while
// every other runtime database stays in the SQLite home.
func TestPrepareSharedStateRuntimeRoutesLogsToConfiguredLogDirLikeRust(t *testing.T) {
	home := t.TempDir()
	logsDir := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("log_dir = "+tomlQuoteString(filepath.ToSlash(logsDir))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prepared, runtime, err := prepareSharedStateRuntime(ctx, home, nil)
	if err != nil {
		t.Fatalf("prepareSharedStateRuntime: %v", err)
	}
	if runtime == nil {
		t.Fatalf("prepareSharedStateRuntime did not own a runtime")
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if filepath.Clean(prepared.ConfiguredLogDir) != filepath.Clean(logsDir) {
		t.Fatalf("ConfiguredLogDir = %q, want %q", prepared.ConfiguredLogDir, logsDir)
	}
	if info, err := os.Stat(filepath.Join(logsDir, state.LogsSQLiteFilename)); err != nil || info.Size() == 0 {
		t.Fatalf("logs database in log_dir = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, state.LogsSQLiteFilename)); !os.IsNotExist(err) {
		t.Fatalf("logs database stayed in the SQLite home: %v", err)
	}
	if info, err := os.Stat(filepath.Join(home, state.StateSQLiteFilename)); err != nil || info.Size() == 0 {
		t.Fatalf("state database in the SQLite home = %v", err)
	}
}

// A managed sqlite_home requirement keeps every database, including logs, in
// the required directory even when log_dir is configured (Rust #55978).
func TestPrepareSharedStateRuntimeKeepsLogsInManagedSQLiteHomeLikeRust(t *testing.T) {
	home := t.TempDir()
	logsDir := filepath.Join(t.TempDir(), "log")
	sqliteHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("log_dir = "+tomlQuoteString(filepath.ToSlash(logsDir))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"),
		[]byte("sqlite_home = "+tomlQuoteString(filepath.ToSlash(sqliteHome))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	prepared, runtime, err := prepareSharedStateRuntime(ctx, home, nil)
	if err != nil {
		t.Fatalf("prepareSharedStateRuntime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	if prepared.ConfiguredLogDir != "" {
		t.Fatalf("ConfiguredLogDir = %q, want empty under a required sqlite_home", prepared.ConfiguredLogDir)
	}
	if info, err := os.Stat(filepath.Join(sqliteHome, state.LogsSQLiteFilename)); err != nil || info.Size() == 0 {
		t.Fatalf("required sqlite_home logs database = %v", err)
	}
	if _, err := os.Stat(filepath.Join(logsDir, state.LogsSQLiteFilename)); !os.IsNotExist(err) {
		t.Fatalf("logs database moved away from the required sqlite_home: %v", err)
	}
}

// Feedback falls back to the previous logs database and attaches it as
// codex-logs-legacy.log (Rust #55978).
func TestFeedbackUploadAttachesLegacySQLiteLogsLikeRust(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	threadID := "legacy-feedback-thread"
	body := "legacy feedback diagnostic"
	now := time.Now().UTC()

	legacyConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	legacyRuntime, err := state.InitStateRuntime(ctx, legacyConfig, "openai")
	if err != nil {
		t.Fatalf("init legacy runtime: %v", err)
	}
	if err := legacyRuntime.InsertLogs(ctx, []state.LogEntry{{
		TS: time.Now().Unix(), Level: "INFO", Target: "legacy", FeedbackLogBody: &body, ThreadID: &threadID,
	}}); err != nil {
		t.Fatalf("insert legacy log: %v", err)
	}
	if err := legacyRuntime.Close(); err != nil {
		t.Fatalf("close legacy runtime: %v", err)
	}

	logsDir := filepath.Join(t.TempDir(), "log")
	runtime, err := state.InitStateRuntime(ctx, legacyConfig.WithLocalLogsHome(logsDir), "openai")
	if err != nil {
		t.Fatalf("init runtime with logs home: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	store := session.NewStore(home)
	threadRouter := NewRouter(store)
	record := &session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID, CreatedAt: now, UpdatedAt: now,
		Metadata: session.Metadata{CWD: home, ModelProvider: "openai"},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := threadRouter.createThreadRollout(record, record.CreatedAt); err != nil {
		t.Fatal(err)
	}
	snapshot := &FeedbackSnapshot{Diagnostics: NewFeedbackDiagnostics(nil)}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:     threadRouter,
		StateRuntime:     runtime,
		ConfiguredLogDir: logsDir,
		Feedback:         snapshot,
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodFeedbackUpload, FeedbackUploadParams{
		Classification: "bug", ThreadID: stringPointerAppserver(threadID), IncludeLogs: true,
	}))
	if response.Error != nil {
		t.Fatalf("feedback upload error = %+v", response.Error)
	}
	prepared := snapshot.LastPrepared
	if prepared == nil {
		t.Fatalf("feedback upload was not prepared")
	}
	legacy := feedbackAttachmentByName(prepared.Attachments, "codex-logs-legacy.log")
	if legacy == nil || !strings.Contains(string(legacy.Buffer), body) {
		t.Fatalf("legacy feedback attachment = %q", feedbackAttachmentBuffer(legacy))
	}
	if legacy.ContentType != "text/plain" {
		t.Fatalf("legacy attachment content type = %q", legacy.ContentType)
	}
}

func tomlQuoteString(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"")
	return "\"" + replacer.Replace(value) + "\""
}
