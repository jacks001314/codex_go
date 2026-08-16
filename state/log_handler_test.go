package state

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

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
	if err := handler.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := runtime.QueryLogs(context.Background(), LogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("filtered rows = %#v", rows)
	}
	for _, row := range rows {
		if row.Target == "codex_otel.log_only" || row.Target == "codex_api::responses_websocket_timing" || (row.Target == "opentelemetry_sdk" && row.Level == "DEBUG") {
			t.Fatalf("filtered row persisted: %#v", row)
		}
	}
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
