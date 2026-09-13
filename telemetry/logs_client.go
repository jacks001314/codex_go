package telemetry

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"codex_go/state"
)

// Rust parity: codex-rs/otel/src/provider.rs's logger provider plus the OTLP
// batch log processor behind it (bounded queue, periodic export, shutdown
// flush). The metrics client in this package follows the same shape.

// DefaultLogsQueueSize mirrors the OTLP batch log processor's default queue.
const DefaultLogsQueueSize = 2048

// LogsClientOptions configures the log pipeline. An empty Endpoint (or an
// exporter that cannot be built) leaves the client disabled.
type LogsClientOptions struct {
	Environment    string
	ServiceName    string
	ServiceVersion string
	Endpoint       string
	Headers        map[string]string
	HTTPClient     HTTPDoer
	// TLS configures the OTLP HTTP transport when no HTTPClient is supplied.
	TLS *OTLPHTTPTLSConfig
	// Protocol selects the OTLP HTTP payload encoding (json by default, or
	// binary for the protobuf body).
	Protocol string
	// Transport selects the OTLP transport: LogsTransportHTTP (the default) or
	// LogsTransportGRPC.
	Transport string
	// ExportInterval is the periodic export cadence. Zero uses the batch
	// processor's default; a negative value disables the background loop so the
	// caller drives Flush directly.
	ExportInterval time.Duration
	Timeout        time.Duration
	// QueueSize bounds the pending records; zero uses DefaultLogsQueueSize.
	QueueSize int
	// Now overrides the clock (tests).
	Now func() time.Time
}

// LogsClient records log records and exports them through an OTLP exporter.
type LogsClient struct {
	exporter       LogsExporter
	scope          OTLPScope
	environment    string
	serviceName    string
	serviceVersion string
	queueSize      int
	now            func() time.Time

	mu       sync.Mutex
	queue    []OTLPLogRecord
	dropped  uint64
	interval time.Duration
	cancel   context.CancelFunc
	done     chan struct{}
	closed   bool
}

// NewLogsClient builds the client. A missing or unsupported exporter leaves it
// disabled, which is what an all-disabled exporter resolves to.
func NewLogsClient(options LogsClientOptions) *LogsClient {
	client := &LogsClient{
		scope:          OTLPScope{Name: LogsScopeName},
		environment:    options.Environment,
		serviceName:    options.ServiceName,
		serviceVersion: options.ServiceVersion,
		queueSize:      options.QueueSize,
		now:            options.Now,
	}
	if client.now == nil {
		client.now = time.Now
	}
	if client.queueSize <= 0 {
		client.queueSize = DefaultLogsQueueSize
	}
	switch options.Transport {
	case LogsTransportGRPC:
		client.exporter = NewOTLPGRPCLogsExporter(OTLPGRPCLogsExporterOptions{
			Endpoint: options.Endpoint,
			Headers:  options.Headers,
			Timeout:  options.Timeout,
			TLS:      options.TLS,
		})
	default:
		client.exporter = NewOTLPLogsExporter(OTLPLogsExporterOptions{
			Endpoint:   options.Endpoint,
			Headers:    options.Headers,
			HTTPClient: options.HTTPClient,
			Timeout:    options.Timeout,
			TLS:        options.TLS,
			Protocol:   options.Protocol,
		})
	}
	if client.exporter == nil {
		return client
	}
	exportInterval := options.ExportInterval
	if exportInterval == 0 {
		exportInterval = DefaultLogsExportInterval
	}
	if exportInterval > 0 {
		client.startExportLoop(exportInterval)
	}
	return client
}

// Enabled reports whether the client will export log records.
func (c *LogsClient) Enabled() bool {
	return c != nil && c.exporter != nil
}

// Emit queues one log record. A full queue drops the record, like the OTLP
// batch processor's bounded queue.
func (c *LogsClient) Emit(record OTLPLogRecord) {
	if c == nil || !c.Enabled() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if len(c.queue) >= c.queueSize {
		c.dropped++
		return
	}
	c.queue = append(c.queue, record)
}

// Dropped reports how many records were dropped because the queue was full.
func (c *LogsClient) Dropped() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropped
}

// Flush exports the queued records.
func (c *LogsClient) Flush(ctx context.Context) error {
	if c == nil || !c.Enabled() {
		return nil
	}
	request := c.snapshot()
	if len(request.ResourceLogs) == 0 {
		return nil
	}
	if err := c.exporter.Export(ctx, request); err != nil {
		logLogsExportFailure(err)
		return err
	}
	return nil
}

// Shutdown stops the export loop and flushes the remaining records.
func (c *LogsClient) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	done := c.done
	c.done = nil
	closed := c.closed
	c.closed = true
	c.mu.Unlock()
	if done != nil && !closed {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	if err := c.Flush(ctx); err != nil {
		return err
	}
	if c.exporter != nil {
		return c.exporter.Close()
	}
	return nil
}

// snapshot drains the queued records into an OTLP batch.
func (c *LogsClient) snapshot() OTLPExportLogsRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.queue) == 0 {
		return OTLPExportLogsRequest{}
	}
	records := append([]OTLPLogRecord(nil), c.queue...)
	c.queue = nil
	return OTLPExportLogsRequest{
		ResourceLogs: []OTLPResourceLogs{{
			Resource:  OTLPResource{Attributes: c.resourceAttributes()},
			ScopeLogs: c.scopeLogs(records),
		}},
	}
}

// scopeLogs groups the drained records the way OpenTelemetry's appender bridge
// reports them: one scope per tracing target, in first-seen order.
func (c *LogsClient) scopeLogs(records []OTLPLogRecord) []OTLPScopeLogs {
	scopes := make([]OTLPScopeLogs, 0, 1)
	index := map[string]int{}
	for _, record := range records {
		name := strings.TrimSpace(record.Target)
		if name == "" {
			name = c.scope.Name
		}
		position, ok := index[name]
		if !ok {
			scopes = append(scopes, OTLPScopeLogs{Scope: OTLPScope{Name: name}})
			position = len(scopes) - 1
			index[name] = position
		}
		scopes[position].LogRecords = append(scopes[position].LogRecords, record)
	}
	return scopes
}

func (c *LogsClient) startExportLoop(interval time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.mu.Lock()
	c.interval = interval
	c.cancel = cancel
	c.done = done
	c.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = c.Flush(ctx)
			}
		}
	}()
}

// resourceAttributes mirrors the metrics pipeline's resource: the service name,
// service.version, and the environment.
func (c *LogsClient) resourceAttributes() []MetricTagValue {
	attributes := []MetricTagValue{}
	if c == nil {
		return attributes
	}
	if name := c.serviceName; name != "" {
		attributes = append(attributes, MetricTagValue{Key: "service.name", Value: name})
	}
	if version := c.serviceVersion; version != "" {
		attributes = append(attributes, MetricTagValue{Key: "service.version", Value: version})
	}
	if environment := c.environment; environment != "" {
		attributes = append(attributes, MetricTagValue{Key: "env", Value: environment})
	}
	return attributes
}

// LogsSlogHandler forwards slog records whose tracing target is exported to an
// OTLP log client, delegating every record to the wrapped handler.
type LogsSlogHandler struct {
	client *LogsClient
	next   slog.Handler
	attrs  []slog.Attr
	groups []string
}

// NewLogsSlogHandler wraps next so exported records also reach the client.
func NewLogsSlogHandler(client *LogsClient, next slog.Handler) *LogsSlogHandler {
	return &LogsSlogHandler{client: client, next: next}
}

// LogExportTargetPrefix mirrors codex-otel's OTEL_TARGET_PREFIX.
const LogExportTargetPrefix = "codex_otel"

// TraceSafeTargetPrefix mirrors codex-otel's OTEL_TRACE_SAFE_TARGET: records
// from that target are exported as spans, not logs.
const TraceSafeTargetPrefix = "codex_otel.trace_safe"

// IsLogExportTarget mirrors codex-otel's targets::is_log_export_target: only
// Codex telemetry targets that are not trace-safe are exported as log records.
func IsLogExportTarget(target string) bool {
	target = strings.TrimSpace(target)
	return strings.HasPrefix(target, LogExportTargetPrefix) && !strings.HasPrefix(target, TraceSafeTargetPrefix)
}

// Enabled reports whether any handler in the chain accepts the level.
func (h *LogsSlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if h == nil {
		return false
	}
	if h.next != nil {
		return h.next.Enabled(ctx, level)
	}
	return true
}

// Handle forwards exported records to the OTLP client and every record to the
// wrapped handler.
func (h *LogsSlogHandler) Handle(ctx context.Context, record slog.Record) error {
	if h == nil {
		return nil
	}
	if h.client != nil && h.client.Enabled() {
		attrs := h.recordAttrs(record)
		target := state.SlogTargetForRecord(record, attrs)
		if IsLogExportTarget(target) {
			severity, text := severityForLevel(record.Level)
			attributes := make([]MetricTagValue, 0, len(attrs))
			keys := make([]string, 0, len(attrs))
			for key := range attrs {
				if key == "target" {
					continue
				}
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				attributes = append(attributes, MetricTagValue{Key: key, Value: attrs[key]})
			}
			h.client.Emit(OTLPLogRecord{
				TimeUnixNano:   timeUnixNanoString(record.Time),
				SeverityNumber: severity,
				SeverityText:   text,
				Body:           record.Message,
				Attributes:     attributes,
				Target:         target,
			})
		}
	}
	if h.next != nil {
		return h.next.Handle(ctx, record)
	}
	return nil
}

// WithAttrs returns a handler that carries the attributes.
func (h *LogsSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if h == nil {
		return nil
	}
	clone := &LogsSlogHandler{client: h.client, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...), groups: append([]string(nil), h.groups...)}
	if h.next != nil {
		clone.next = h.next.WithAttrs(attrs)
	}
	return clone
}

// WithGroup returns a handler that scopes future attributes to the group.
func (h *LogsSlogHandler) WithGroup(name string) slog.Handler {
	if h == nil {
		return nil
	}
	clone := &LogsSlogHandler{client: h.client, attrs: append([]slog.Attr(nil), h.attrs...), groups: append(append([]string(nil), h.groups...), name)}
	if h.next != nil {
		clone.next = h.next.WithGroup(name)
	}
	return clone
}

// recordAttrs flattens the handler's attributes with the record's own.
func (h *LogsSlogHandler) recordAttrs(record slog.Record) map[string]string {
	attrs := append([]slog.Attr(nil), h.attrs...)
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})
	return state.FlattenSlogAttrs(h.groups, attrs)
}
