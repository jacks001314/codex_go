package telemetry

import (
	"context"
	"time"
)

// Rust parity: codex-rs/otel/src/events/session_telemetry.rs (SessionTelemetry's
// metadata and the log_event! / trace_event! / log_and_trace_event! macros) and
// events/shared.rs.
//
// Every session metric has a matching diagnostic record: the log-only half goes
// to the logs pipeline (a record whose tracing target is `codex_otel.log_only`),
// and the trace-safe half becomes an event on the span the caller is running
// inside. Rust's tracing layer drops an event that has no enclosing span, so a
// trace record outside a span is not exported either.

// Target names mirror codex-otel's targets.rs.
const (
	OtelLogOnlyTarget   = "codex_otel.log_only"
	OtelTraceSafeTarget = "codex_otel.trace_safe"
)

// SessionTelemetryMetadata mirrors codex-otel's SessionTelemetryMetadata: the
// per-conversation identity every record carries.
type SessionTelemetryMetadata struct {
	// ConversationID is Rust's conversation.id (the thread id).
	ConversationID string
	// AgentName is Rust's agent_name: the canonical agent path, or the thread's
	// legacy nickname/id.
	AgentName string
	// AuthMode is absent when the session has no auth mode.
	AuthMode string
	// AccountID / AccountEmail are absent for API-key sessions.
	AccountID    string
	AccountEmail string
	// Originator is the bounded client originator.
	Originator string
	// Model and Slug name the model the session runs.
	Model string
	Slug  string
	// LogUserPrompts gates whether prompt text may reach the log record.
	LogUserPrompts bool
	// AppVersion is the Codex version reporting the record.
	AppVersion string
	// TerminalType is the client's terminal name.
	TerminalType string
}

// LogRecordSink receives the log-only records the session telemetry emits.
// Rust's records reach the exporter through the tracing log layer; Go hands
// them to the logs client directly so per-call telemetry never reaches the
// process logger (Rust's stderr layer is filtered by RUST_LOG and its log DB
// turns these targets off).
type LogRecordSink interface {
	Emit(record OTLPLogRecord)
}

// SessionTelemetry emits Rust's session telemetry records.
type SessionTelemetry struct {
	Metadata SessionTelemetryMetadata
	// Logs receives the log-only half; nil drops it, like a process without an
	// OTEL log layer.
	Logs LogRecordSink
	// Clock overrides the record timestamp (tests).
	Clock func() time.Time
}

// NewSessionTelemetry builds an emitter for one conversation's metadata.
func NewSessionTelemetry(metadata SessionTelemetryMetadata) *SessionTelemetry {
	return &SessionTelemetry{Metadata: metadata}
}

// LogAndTraceEvent emits Rust's log_and_trace_event!: the diagnostic record to
// the logs pipeline and the trace-safe event to the span in ctx, when there is
// one.
func (t *SessionTelemetry) LogAndTraceEvent(ctx context.Context, eventName string, fields map[string]string, logOnly map[string]string, traceOnly map[string]string) {
	t.LogEvent(ctx, eventName, fields, logOnly)
	t.TraceEvent(ctx, eventName, fields, traceOnly)
}

// LogEvent emits Rust's log_event!: a `codex_otel.log_only` record carrying the
// event fields plus the session identity. The record has no message body, as
// Rust's events do not set a `message` field.
func (t *SessionTelemetry) LogEvent(ctx context.Context, eventName string, fields map[string]string, logOnly map[string]string) {
	if t == nil || t.Logs == nil {
		return
	}
	record := make(map[string]string, 12+len(fields)+len(logOnly))
	record["event.name"] = eventName
	t.addLogMetadata(record)
	for key, value := range fields {
		record[key] = value
	}
	for key, value := range logOnly {
		record[key] = value
	}
	t.Logs.Emit(OTLPLogRecord{
		TimeUnixNano:   timeUnixNanoString(t.now()),
		SeverityNumber: otlpSeverityInfo,
		SeverityText:   "INFO",
		// Rust's events carry no `message` field, so the record has no body.
		Target:     OtelLogOnlyTarget,
		Attributes: sortedMetricTags(record),
	})
}

// TraceEvent records the trace-safe half on the span carried in ctx. Without an
// enclosing span the record is dropped, mirroring Rust's tracing layer.
func (t *SessionTelemetry) TraceEvent(ctx context.Context, eventName string, fields map[string]string, traceOnly map[string]string) {
	if t == nil {
		return
	}
	span := SpanFromContext(ctx)
	if span == nil {
		return
	}
	attributes := map[string]string{
		"level":      "INFO",
		"target":     OtelTraceSafeTarget,
		"event.name": eventName,
	}
	t.addTraceMetadata(attributes)
	for key, value := range fields {
		attributes[key] = value
	}
	for key, value := range traceOnly {
		attributes[key] = value
	}
	span.AddEvent(eventName, attributes, t.now())
}

// addLogMetadata adds the common fields Rust's log_event! prepends.
func (t *SessionTelemetry) addLogMetadata(fields map[string]string) {
	fields["event.timestamp"] = t.timestamp()
	fields["conversation.id"] = t.Metadata.ConversationID
	fields["app.version"] = t.Metadata.AppVersion
	if t.Metadata.AuthMode != "" {
		fields["auth_mode"] = t.Metadata.AuthMode
	}
	fields["originator"] = t.Metadata.Originator
	if t.Metadata.AccountID != "" {
		fields["user.account_id"] = t.Metadata.AccountID
	}
	if t.Metadata.AccountEmail != "" {
		fields["user.email"] = t.Metadata.AccountEmail
	}
	fields["terminal.type"] = t.Metadata.TerminalType
	fields["model"] = t.Metadata.Model
	fields["slug"] = t.Metadata.Slug
}

// addTraceMetadata adds the common fields Rust's trace_event! prepends. The
// account identity stays on the log record only, like Rust.
func (t *SessionTelemetry) addTraceMetadata(attributes map[string]string) {
	attributes["event.timestamp"] = t.timestamp()
	attributes["conversation.id"] = t.Metadata.ConversationID
	attributes["app.version"] = t.Metadata.AppVersion
	if t.Metadata.AuthMode != "" {
		attributes["auth_mode"] = t.Metadata.AuthMode
	}
	attributes["originator"] = t.Metadata.Originator
	attributes["terminal.type"] = t.Metadata.TerminalType
	attributes["model"] = t.Metadata.Model
	attributes["slug"] = t.Metadata.Slug
}

// timestamp mirrors events::shared::timestamp: RFC 3339 with milliseconds.
func (t *SessionTelemetry) timestamp() string {
	return t.now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func (t *SessionTelemetry) now() time.Time {
	if t != nil && t.Clock != nil {
		return t.Clock()
	}
	return time.Now()
}

// sessionTelemetrySpanKey carries the span whose events a trace-safe record
// lands on.
type sessionTelemetrySpanKey struct{}

// WithSpan returns a context carrying the span, so later trace-safe records
// attach to it (Rust's current-span lookup).
func WithSpan(ctx context.Context, span *Span) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if span == nil {
		return ctx
	}
	return context.WithValue(ctx, sessionTelemetrySpanKey{}, span)
}

// SpanFromContext returns the span carried by ctx, or nil.
func SpanFromContext(ctx context.Context) *Span {
	if ctx == nil {
		return nil
	}
	span, _ := ctx.Value(sessionTelemetrySpanKey{}).(*Span)
	return span
}
