package state

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

type recordingLogWriteFailureReporter struct {
	diagnostics []string
}

func (r *recordingLogWriteFailureReporter) ReportLogWriteFailure(diagnostic string) {
	r.diagnostics = append(r.diagnostics, diagnostic)
}

func TestLogDBHandlerPersistsStructuredRecordsAndPreservesNextHandler(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	var stderr bytes.Buffer
	next := slog.NewTextHandler(&stderr, &slog.HandlerOptions{Level: slog.LevelInfo, AddSource: true})
	handler := NewLogDBHandlerWithConfig(runtime, next, LogSinkQueueConfig{QueueCapacity: 8, BatchSize: 2, FlushInterval: time.Hour})
	logger := slog.New(handler).With("thread_id", "thread-1")
	logger.Debug("debug body", "target", "codex_test", "value", 1)
	logger.Info("info body", "target", "codex_test", "value", 2)
	if err := handler.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := handler.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := runtime.QueryLogs(context.Background(), LogQuery{ThreadIDs: []string{"thread-1"}})
	if err != nil || len(rows) != 2 {
		t.Fatalf("persisted handler logs = %#v, %v", rows, err)
	}
	if rows[0].Target != "codex_test" || rows[0].Level != "DEBUG" || rows[0].Message == nil || !strings.Contains(*rows[0].Message, "debug body") {
		t.Fatalf("debug row = %#v", rows[0])
	}
	if !strings.Contains(stderr.String(), "info body") || strings.Contains(stderr.String(), "debug body") {
		t.Fatalf("next handler output = %q", stderr.String())
	}
}

func TestLogDBHandlerDropsFilteredRecords(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	handler := NewLogDBHandlerWithConfig(runtime, nil, LogSinkQueueConfig{QueueCapacity: 8, BatchSize: 128, FlushInterval: time.Hour})
	logger := slog.New(handler)
	logger.Debug("drop sdk", "target", "opentelemetry_sdk")
	logger.Info("retain sdk", "target", "opentelemetry_sdk")
	logger.Debug("drop websocket", "target", "codex_api::responses_websocket_timing")
	logger.Info("drop log-only audit", "target", "codex_otel.log_only")
	logger.Info("retain audit", "target", "codex_otel.network_proxy")
	// Rust #40658: every SQLx diagnostic is excluded so SQLite writes cannot
	// generate more log writes, while a similarly named target stays eligible.
	logger.Warn("drop sqlx query", "target", "sqlx::query")
	logger.Warn("drop sqlx other", "target", "sqlx::other")
	logger.Warn("drop sqlx core", "target", "sqlx_core::connection")
	logger.Warn("drop sqlx sqlite", "target", "sqlx_sqlite::statement")
	logger.Info("retain sqlx application", "target", "sqlx_application")
	// Rust #49414: guard/trigger TRACE events are dropped while their DEBUG events
	// survive, and the shutdown target keeps its TRACE events.
	logger.Log(context.Background(), slog.Level(-8), "drop guard trace", "target", "tokio_graceful::guard")
	logger.Debug("retain guard debug", "target", "tokio_graceful::guard")
	logger.Log(context.Background(), slog.Level(-8), "drop trigger trace", "target", "tokio_graceful::trigger")
	logger.Debug("retain trigger debug", "target", "tokio_graceful::trigger")
	logger.Log(context.Background(), slog.Level(-8), "retain shutdown trace", "target", "tokio_graceful::shutdown")
	if err := handler.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := runtime.QueryLogs(context.Background(), LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 6 {
		t.Fatalf("filtered rows = %#v", rows)
	}
	for _, row := range rows {
		if row.Target == "codex_otel.log_only" || row.Target == "codex_api::responses_websocket_timing" || (row.Target == "opentelemetry_sdk" && row.Level == "DEBUG") {
			t.Fatalf("filtered row persisted: %#v", row)
		}
		if strings.HasPrefix(row.Target, "sqlx::") || strings.HasPrefix(row.Target, "sqlx_core::") || strings.HasPrefix(row.Target, "sqlx_sqlite::") {
			t.Fatalf("SQLx diagnostic persisted: %#v", row)
		}
		if (row.Target == "tokio_graceful::guard" || row.Target == "tokio_graceful::trigger") && row.Level != "DEBUG" {
			t.Fatalf("guard/trigger trace persisted: %#v", row)
		}
	}
	if !logRowsContainTarget(rows, "sqlx_application") {
		t.Fatalf("sqlx_application log was dropped: %#v", rows)
	}
}

func logRowsContainTarget(rows []LogRow, target string) bool {
	for _, row := range rows {
		if row.Target == target {
			return true
		}
	}
	return false
}

func TestInstallLogDBHandlerRestoresPreviousLogger(t *testing.T) {
	runtime := newGoalTestRuntime(t)
	previous := slog.Default()
	installation := InstallLogDBHandler(runtime)
	if slog.Default() == previous {
		t.Fatal("logger was not installed")
	}
	slog.Info("installed log", "target", "test", "thread_id", "thread-install")
	if err := installation.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if slog.Default() != previous {
		t.Fatal("previous logger was not restored")
	}
	rows, err := runtime.QueryLogs(context.Background(), LogQuery{ThreadIDs: []string{"thread-install"}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("installed logs = %#v, %v", rows, err)
	}
}

// Rust #47887: a failed SQLite flush retains its diagnostic, reports the
// failure, and replays it when the client reporter is attached later.
func TestLogDBHandlerReportsAndReplaysWriteFailureLikeRust(t *testing.T) {
	newFailingHandler := func(t *testing.T) (*LogDBHandler, *recordingLogWriteFailureReporter) {
		t.Helper()
		runtime := newGoalTestRuntime(t)
		handler := NewLogDBHandlerWithConfig(runtime, nil, LogSinkQueueConfig{QueueCapacity: 8, BatchSize: 1, FlushInterval: time.Hour})
		if err := runtime.logsDB.Close(); err != nil {
			t.Fatalf("close logs DB: %v", err)
		}
		return handler, &recordingLogWriteFailureReporter{}
	}

	t.Run("reporter attached before failure", func(t *testing.T) {
		handler, reporter := newFailingHandler(t)
		defer func() { _ = handler.Close(context.Background()) }()
		handler.SetFailureReporter(reporter)
		slog.New(handler).Info("lost log", "target", "codex_test")
		if err := handler.Flush(context.Background()); err != nil {
			t.Fatalf("flush: %v", err)
		}
		if len(reporter.diagnostics) != 1 || !strings.Contains(reporter.diagnostics[0], "failed to flush logs to SQLite") {
			t.Fatalf("reported diagnostics = %#v", reporter.diagnostics)
		}
		if !handler.HasWriteFailure() || !strings.Contains(handler.LastWriteFailure(), "entries=1") {
			t.Fatalf("retained failure = %q", handler.LastWriteFailure())
		}
	})

	t.Run("reporter attached after failure", func(t *testing.T) {
		handler, reporter := newFailingHandler(t)
		defer func() { _ = handler.Close(context.Background()) }()
		slog.New(handler).Info("lost before reporter", "target", "codex_test")
		if err := handler.Flush(context.Background()); err != nil {
			t.Fatalf("flush: %v", err)
		}
		if len(reporter.diagnostics) != 0 {
			t.Fatalf("unattached reporter received %#v", reporter.diagnostics)
		}
		handler.SetFailureReporter(reporter)
		if len(reporter.diagnostics) != 1 || !strings.Contains(reporter.diagnostics[0], "failed to flush logs to SQLite") {
			t.Fatalf("replayed diagnostics = %#v", reporter.diagnostics)
		}
	})
}

// Mirrors Rust's `post_sampling_token_estimate_is_disabled_by_always_on_sinks`
// (#50054): the always-on log-DB sink filter must never persist the
// post-sampling token estimate target, checked directly on the sink filter.
func TestPostSamplingTokenEstimateIsDisabledByAlwaysOnSinks(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		if persistSlogRecord("codex_core::post_sampling_token_estimate", level) {
			t.Fatalf("persistSlogRecord(post_sampling, %v) = true, want false", level)
		}
	}
	// A regular target keeps being persisted.
	if !persistSlogRecord("codex_test", slog.LevelInfo) {
		t.Fatal("persistSlogRecord(codex_test) = false, want true")
	}
}
