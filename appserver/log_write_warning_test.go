package appserver

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/state"
)

// Rust #47887: the first SQLite diagnostic-log write failure warns once, and a
// failure that happened before the notification sink was attached is replayed.
func TestLogDBWriteFailureWarnsOnceLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		feedbackEnabled bool
		wantMessage     string
	}{
		{name: "feedback enabled", feedbackEnabled: true, wantMessage: logWriteWarningWithFeedback},
		{name: "feedback disabled", feedbackEnabled: false, wantMessage: logWriteWarningNoFeedback},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			if !testCase.feedbackEnabled {
				if err := os.WriteFile(config.ConfigPath(home), []byte("[feedback]\nenabled = false\n"), 0o600); err != nil {
					t.Fatalf("write config: %v", err)
				}
			}
			sqliteConfig, err := state.NewSqliteConfig(home)
			if err != nil {
				t.Fatalf("NewSqliteConfig: %v", err)
			}
			stateRuntime, err := state.InitStateRuntime(context.Background(), sqliteConfig, "openai")
			if err != nil {
				t.Fatalf("InitStateRuntime: %v", err)
			}
			handler := state.NewLogDBHandlerWithConfig(stateRuntime, nil, state.LogSinkQueueConfig{
				QueueCapacity: 8,
				BatchSize:     1,
				FlushInterval: time.Hour,
			})
			defer func() { _ = handler.Close(context.Background()) }()
			if err := stateRuntime.Close(); err != nil {
				t.Fatalf("close state runtime: %v", err)
			}

			// Fail before the router has a notification sink.
			slog.New(handler).Info("unattached lost log", "target", "codex_test")
			if err := handler.Flush(context.Background()); err != nil {
				t.Fatalf("flush unattached failure: %v", err)
			}
			if !handler.HasWriteFailure() {
				t.Fatal("log handler did not retain the write failure")
			}

			router := NewRuntimeRouter(RuntimeServices{
				Config:       config.NewConfigService(home),
				LogDBHandler: handler,
			})
			sink := NewNotificationBuffer()
			router.SetNotificationSink(sink)

			warningCount := func() int {
				count := 0
				for _, notification := range sink.List() {
					if notification.Method != NotificationWarning {
						continue
					}
					warning, _ := notification.Params.(*WarningNotification)
					if warning != nil && warning.Message == testCase.wantMessage && warning.ThreadID == nil {
						count++
					}
				}
				return count
			}
			if count := warningCount(); count != 1 {
				t.Fatalf("replayed warnings = %d, want 1; notifications = %#v", count, sink.List())
			}

			slog.New(handler).Info("later lost log", "target", "codex_test")
			if err := handler.Flush(context.Background()); err != nil {
				t.Fatalf("flush later failure: %v", err)
			}
			if count := warningCount(); count != 1 {
				t.Fatalf("warning after later failure = %d total, want the single replayed warning", count)
			}
		})
	}
}
