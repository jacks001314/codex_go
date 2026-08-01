package state

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultLogQueueCapacity = 512
	defaultLogBatchSize     = 128
	defaultLogFlushInterval = 2 * time.Second
)

type LogSinkQueueConfig struct {
	QueueCapacity int
	BatchSize     int
	FlushInterval time.Duration
}

func (c LogSinkQueueConfig) normalized() LogSinkQueueConfig {
	if c.QueueCapacity < 1 {
		c.QueueCapacity = defaultLogQueueCapacity
	}
	if c.BatchSize < 1 {
		c.BatchSize = defaultLogBatchSize
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = defaultLogFlushInterval
	}
	return c
}

type logDBCommand struct {
	entry *LogEntry
	reply chan struct{}
	close bool
}

type logDBSink struct {
	mu       sync.Mutex
	commands chan logDBCommand
	done     chan struct{}
	closed   bool
}

type LogDBHandler struct {
	next        slog.Handler
	sink        *logDBSink
	processUUID string
	attrs       []slog.Attr
	groups      []string
}

func NewLogDBHandler(runtime *StateRuntime, next slog.Handler) *LogDBHandler {
	return NewLogDBHandlerWithConfig(runtime, next, LogSinkQueueConfig{})
}

func NewLogDBHandlerWithConfig(runtime *StateRuntime, next slog.Handler, config LogSinkQueueConfig) *LogDBHandler {
	config = config.normalized()
	sink := &logDBSink{
		commands: make(chan logDBCommand, config.QueueCapacity),
		done:     make(chan struct{}),
	}
	go sink.run(runtime, config)
	return &LogDBHandler{
		next:        next,
		sink:        sink,
		processUUID: fmt.Sprintf("pid:%d:%s", os.Getpid(), uuid.NewString()),
	}
}

func (h *LogDBHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h != nil && h.sink != nil
}

func (h *LogDBHandler) Handle(ctx context.Context, record slog.Record) error {
	if h == nil {
		return nil
	}
	var nextErr error
	if h.next != nil && h.next.Enabled(ctx, record.Level) {
		nextErr = h.next.Handle(ctx, record)
	}
	entry := h.entry(record)
	if entry == nil {
		return nextErr
	}
	h.sink.tryEntry(*entry)
	return nextErr
}

func (h *LogDBHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if h == nil {
		return h
	}
	clone := *h
	clone.attrs = append(append([]slog.Attr(nil), h.attrs...), attrs...)
	if h.next != nil {
		clone.next = h.next.WithAttrs(attrs)
	}
	return &clone
}

func (h *LogDBHandler) WithGroup(name string) slog.Handler {
	if h == nil {
		return h
	}
	clone := *h
	clone.groups = append(append([]string(nil), h.groups...), name)
	if h.next != nil {
		clone.next = h.next.WithGroup(name)
	}
	return &clone
}

func (h *LogDBHandler) Flush(ctx context.Context) error {
	if h == nil || h.sink == nil {
		return nil
	}
	return h.sink.flush(ctx)
}

func (h *LogDBHandler) Close(ctx context.Context) error {
	if h == nil || h.sink == nil {
		return nil
	}
	return h.sink.close(ctx)
}

func (h *LogDBHandler) entry(record slog.Record) *LogEntry {
	attrs := append([]slog.Attr(nil), h.attrs...)
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})
	flat := flattenSlogAttrs(h.groups, attrs)
	target := flat["target"]
	threadID := stringPointerIfPresent(flat, "thread_id")
	modulePath, file, line := slogSource(record.PC)
	if target == "" && modulePath != nil {
		target = slogTargetFromFunction(*modulePath)
	}
	if target == "" {
		target = "log"
	}
	if !persistSlogRecord(target, record.Level) {
		return nil
	}
	body := formatSlogFeedbackBody(record.Message, flat)
	now := record.Time
	if now.IsZero() {
		now = time.Now()
	}
	level := strings.ToUpper(record.Level.String())
	processUUID := h.processUUID
	return &LogEntry{
		TS:              now.Unix(),
		TSNanos:         int64(now.Nanosecond()),
		Level:           level,
		Target:          target,
		Message:         stringPointerIfNotEmpty(record.Message),
		FeedbackLogBody: &body,
		ThreadID:        threadID,
		ProcessUUID:     &processUUID,
		ModulePath:      modulePath,
		File:            file,
		Line:            line,
	}
}

func persistSlogRecord(target string, level slog.Level) bool {
	switch target {
	case "log", "codex_otel.log_only", "codex_otel.trace_safe", "codex_api::responses_websocket_timing", "codex_core::post_sampling_token_estimate":
		return false
	case "hyper_util":
		return level >= slog.LevelWarn
	case "rmcp::service":
		return level >= slog.LevelInfo
	case "opentelemetry_sdk":
		return level >= slog.LevelInfo
	default:
		return true
	}
}

func flattenSlogAttrs(groups []string, attrs []slog.Attr) map[string]string {
	result := make(map[string]string)
	var visit func([]string, slog.Attr)
	visit = func(prefix []string, attr slog.Attr) {
		attr.Value = attr.Value.Resolve()
		if attr.Equal(slog.Attr{}) {
			return
		}
		if attr.Value.Kind() == slog.KindGroup {
			next := prefix
			if attr.Key != "" {
				next = append(append([]string(nil), prefix...), attr.Key)
			}
			for _, child := range attr.Value.Group() {
				visit(next, child)
			}
			return
		}
		keyParts := append(append([]string(nil), prefix...), attr.Key)
		key := strings.Join(keyParts, ".")
		result[key] = slogValueString(attr.Value)
		if attr.Key == "thread_id" {
			result["thread_id"] = result[key]
		}
		if attr.Key == "target" {
			result["target"] = result[key]
		}
	}
	for _, attr := range attrs {
		visit(groups, attr)
	}
	return result
}

func slogValueString(value slog.Value) string {
	switch value.Kind() {
	case slog.KindString:
		return value.String()
	case slog.KindTime:
		return value.Time().Format(time.RFC3339Nano)
	case slog.KindDuration:
		return value.Duration().String()
	default:
		return fmt.Sprint(value.Any())
	}
}

func formatSlogFeedbackBody(message string, attrs map[string]string) string {
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		if key == "target" || key == "thread_id" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var body strings.Builder
	body.WriteString(message)
	for _, key := range keys {
		if body.Len() > 0 {
			body.WriteByte(' ')
		}
		body.WriteString(key)
		body.WriteByte('=')
		body.WriteString(attrs[key])
	}
	return body.String()
}

func slogSource(pc uintptr) (*string, *string, *int64) {
	if pc == 0 {
		return nil, nil, nil
	}
	frames := runtime.CallersFrames([]uintptr{pc})
	frame, _ := frames.Next()
	modulePath := stringPointerIfNotEmpty(frame.Function)
	file := stringPointerIfNotEmpty(frame.File)
	var line *int64
	if frame.Line > 0 {
		value := int64(frame.Line)
		line = &value
	}
	return modulePath, file, line
}

func slogTargetFromFunction(function string) string {
	if index := strings.LastIndex(function, "/"); index >= 0 {
		function = function[index+1:]
	}
	if index := strings.Index(function, "."); index >= 0 {
		return function[:index]
	}
	return function
}

func stringPointerIfPresent(values map[string]string, key string) *string {
	value, exists := values[key]
	if !exists {
		return nil
	}
	return &value
}

func stringPointerIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (s *logDBSink) tryEntry(entry LogEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.commands <- logDBCommand{entry: &entry}:
	default:
	}
}

func (s *logDBSink) flush(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	reply := make(chan struct{})
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	select {
	case s.commands <- logDBCommand{reply: reply}:
		s.mu.Unlock()
	case <-ctx.Done():
		s.mu.Unlock()
		return ctx.Err()
	}
	select {
	case <-reply:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *logDBSink) close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		select {
		case <-s.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.closed = true
	select {
	case s.commands <- logDBCommand{close: true}:
		s.mu.Unlock()
	case <-ctx.Done():
		s.mu.Unlock()
		return ctx.Err()
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *logDBSink) run(runtime *StateRuntime, config LogSinkQueueConfig) {
	defer close(s.done)
	buffer := make([]LogEntry, 0, config.BatchSize)
	ticker := time.NewTicker(config.FlushInterval)
	defer ticker.Stop()
	flush := func() {
		if len(buffer) == 0 {
			return
		}
		entries := append([]LogEntry(nil), buffer...)
		buffer = buffer[:0]
		_ = runtime.InsertLogs(context.Background(), entries)
	}
	for {
		select {
		case command := <-s.commands:
			if command.entry != nil {
				buffer = append(buffer, *command.entry)
				if len(buffer) >= config.BatchSize {
					flush()
				}
			}
			if command.reply != nil {
				flush()
				close(command.reply)
			}
			if command.close {
				flush()
				return
			}
		case <-ticker.C:
			flush()
		}
	}
}

type LogDBInstallation struct {
	Handler   *LogDBHandler
	previous  *slog.Logger
	installed *slog.Logger
	closeOnce sync.Once
	closeErr  error
}

func InstallLogDBHandler(runtime *StateRuntime) *LogDBInstallation {
	previous := slog.Default()
	next := previous.Handler()
	// slog.SetDefault redirects the standard log package through the installed
	// logger. Retaining slog's private defaultHandler would therefore recurse.
	if fmt.Sprintf("%T", next) == "*slog.defaultHandler" {
		next = slog.NewTextHandler(os.Stderr, nil)
	}
	handler := NewLogDBHandler(runtime, next)
	installed := slog.New(handler)
	slog.SetDefault(installed)
	return &LogDBInstallation{Handler: handler, previous: previous, installed: installed}
}

func (i *LogDBInstallation) Close(ctx context.Context) error {
	if i == nil {
		return nil
	}
	i.closeOnce.Do(func() {
		if slog.Default() == i.installed {
			slog.SetDefault(i.previous)
		}
		i.closeErr = i.Handler.Close(ctx)
	})
	return i.closeErr
}
