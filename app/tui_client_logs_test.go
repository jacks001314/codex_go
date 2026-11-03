package app

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestTUIClientLogBufferKeepsNewestWindow(t *testing.T) {
	buffer := newTUIClientLogBuffer(8)
	buffer.Write([]byte("abcdef"))
	buffer.Write([]byte("ghij"))
	if got := string(buffer.Snapshot()); got != "cdefghij" {
		t.Fatalf("snapshot = %q", got)
	}
}

func TestTUIClientLogHandlerCapturesAndForwards(t *testing.T) {
	var forwarded bytes.Buffer
	buffer := newTUIClientLogBuffer(1024)
	handler := &tuiClientLogHandler{
		buf:  buffer,
		next: slog.NewTextHandler(&forwarded, &slog.HandlerOptions{Level: slog.LevelDebug}),
	}
	logger := slog.New(handler.WithGroup("tui").WithAttrs([]slog.Attr{slog.String("session", "abc")}))
	logger.Info("staged diagnostics", "count", 3)

	captured := string(buffer.Snapshot())
	if !strings.Contains(captured, "tui staged diagnostics") ||
		!strings.Contains(captured, "session=abc") ||
		!strings.Contains(captured, "count=3") {
		t.Fatalf("captured = %q", captured)
	}
	if !strings.Contains(forwarded.String(), "staged diagnostics") {
		t.Fatalf("record was not forwarded: %q", forwarded.String())
	}
}

func TestInstallTUIClientLogCaptureCapturesAndRestores(t *testing.T) {
	before := slog.Default()
	restore := InstallTUIClientLogCapture()
	slog.Warn("remote tui diagnostic line")
	restore()
	if slog.Default() != before {
		t.Fatal("slog default was not restored")
	}
	if tail := string(TUIClientLogTail()); !strings.Contains(tail, "remote tui diagnostic line") {
		t.Fatalf("tail = %q", tail)
	}
}

func TestTUIClientLogHandlerHonorsWrappedLevel(t *testing.T) {
	buffer := newTUIClientLogBuffer(1024)
	handler := &tuiClientLogHandler{buf: buffer, next: slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})}
	if handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("wrapped handler should reject info records")
	}
}
