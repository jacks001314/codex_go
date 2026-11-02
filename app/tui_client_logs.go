package app

// TUI client log capture.
//
// Rust #47889 (codex-rs/tui/src/app/feedback_upload.rs) attaches the TUI
// process's own diagnostics to a feedback upload by staging them on the
// app-server host: the TUI and the app-server are separate processes, so the
// app-server can only read files that exist on its own filesystem. This file
// provides the Go equivalent of the `CodexFeedback` snapshot the Rust TUI
// stages as `client-logs.txt`.
//
// The capture is a bounded in-memory ring installed in front of the process
// default logger for the lifetime of a remote TUI session. It never replaces the
// existing handler, so the terminal/log database behaviour is unchanged.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"codex_go/state"
)

// TUIClientLogMaxBytes matches Rust's MAX_CLIENT_LOG_SIZE. Base64 adds only a
// third to this size, which keeps the fs/writeFile frame well below the
// app-server's 16 MiB message limit.
const TUIClientLogMaxBytes = 1024 * 1024

type tuiClientLogBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newTUIClientLogBuffer(max int) *tuiClientLogBuffer {
	if max <= 0 {
		max = TUIClientLogMaxBytes
	}
	return &tuiClientLogBuffer{max: max}
}

func (b *tuiClientLogBuffer) Write(data []byte) (int, error) {
	if b == nil || len(data) == 0 {
		return 0, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, data...)
	if overflow := len(b.buf) - b.max; overflow > 0 {
		// Rust stage_tui_client_logs drains the oldest bytes so the attachment
		// keeps the newest window of the session log.
		b.buf = append(b.buf[:0], b.buf[overflow:]...)
	}
	return len(data), nil
}

// Snapshot returns a copy of the retained log tail.
func (b *tuiClientLogBuffer) Snapshot() []byte {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.buf) == 0 {
		return nil
	}
	return append([]byte(nil), b.buf...)
}

// tuiClientLogHandler tees records into the capture buffer and forwards them to
// the wrapped handler unchanged.
type tuiClientLogHandler struct {
	buf    *tuiClientLogBuffer
	next   slog.Handler
	attrs  []slog.Attr
	groups []string
}

func (h *tuiClientLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.next != nil {
		return h.next.Enabled(ctx, level)
	}
	return true
}

func (h *tuiClientLogHandler) Handle(ctx context.Context, record slog.Record) error {
	h.buf.Write([]byte(formatTUIClientLogRecord(h.groups, h.attrs, record)))
	if h.next == nil {
		return nil
	}
	return h.next.Handle(ctx, record)
}

func (h *tuiClientLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	clone := &tuiClientLogHandler{
		buf:    h.buf,
		next:   h.next,
		attrs:  append(append([]slog.Attr(nil), h.attrs...), attrs...),
		groups: h.groups,
	}
	if h.next != nil {
		clone.next = h.next.WithAttrs(attrs)
	}
	return clone
}

func (h *tuiClientLogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	clone := &tuiClientLogHandler{
		buf:    h.buf,
		next:   h.next,
		attrs:  h.attrs,
		groups: append(append([]string(nil), h.groups...), name),
	}
	if h.next != nil {
		clone.next = h.next.WithGroup(name)
	}
	return clone
}

func formatTUIClientLogRecord(groups []string, attrs []slog.Attr, record slog.Record) string {
	var builder strings.Builder
	stamp := record.Time
	if stamp.IsZero() {
		stamp = time.Now()
	}
	builder.WriteString(stamp.Format(time.RFC3339Nano))
	builder.WriteByte(' ')
	builder.WriteString(strings.ToUpper(record.Level.String()))
	builder.WriteByte(' ')
	if prefix := strings.Join(groups, "."); prefix != "" {
		builder.WriteString(prefix)
		builder.WriteByte(' ')
	}
	builder.WriteString(record.Message)
	for _, attr := range attrs {
		fmt.Fprintf(&builder, " %s=%v", attr.Key, attr.Value.Any())
	}
	record.Attrs(func(attr slog.Attr) bool {
		fmt.Fprintf(&builder, " %s=%v", attr.Key, attr.Value.Any())
		return true
	})
	builder.WriteByte('\n')
	return builder.String()
}

var (
	tuiClientLogsMu       sync.Mutex
	tuiClientLogsBuffer   *tuiClientLogBuffer
	tuiClientLogsInstalls int
)

// InstallTUIClientLogCapture starts capturing the process default logger and
// returns a restore function. Nested installs share one buffer so a session
// restart keeps the earlier log tail.
func InstallTUIClientLogCapture() (restore func()) {
	tuiClientLogsMu.Lock()
	if tuiClientLogsBuffer == nil {
		tuiClientLogsBuffer = newTUIClientLogBuffer(TUIClientLogMaxBytes)
	}
	buffer := tuiClientLogsBuffer
	tuiClientLogsInstalls++
	tuiClientLogsMu.Unlock()

	previous := slog.Default()
	// slog.SetDefault redirects the standard log package through the installed
	// logger, so chaining slog's private default handler would recurse
	// (state.SlogTerminalHandler documents the same hazard).
	slog.SetDefault(slog.New(&tuiClientLogHandler{buf: buffer, next: state.SlogTerminalHandler()}))

	var once sync.Once
	return func() {
		once.Do(func() {
			if slog.Default().Handler() != nil {
				slog.SetDefault(previous)
			}
			tuiClientLogsMu.Lock()
			if tuiClientLogsInstalls > 0 {
				tuiClientLogsInstalls--
			}
			tuiClientLogsMu.Unlock()
		})
	}
}

// TUIClientLogTail returns the newest captured client log bytes, or nil when no
// capture is installed or nothing has been logged yet.
func TUIClientLogTail() []byte {
	tuiClientLogsMu.Lock()
	buffer := tuiClientLogsBuffer
	tuiClientLogsMu.Unlock()
	if buffer == nil {
		return nil
	}
	return buffer.Snapshot()
}
