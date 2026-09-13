package telemetry

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

// Rust parity: codex-rs/otel/src/provider.rs's tracer provider and the OTLP
// batch span processor behind it, plus the span attributes the provider applies
// to every exported span.

// DefaultTracesQueueSize mirrors the OTLP batch span processor's default queue.
const DefaultTracesQueueSize = 2048

// TracesClientOptions configures the tracing pipeline.
type TracesClientOptions struct {
	Environment    string
	ServiceName    string
	ServiceVersion string
	Endpoint       string
	Headers        map[string]string
	HTTPClient     HTTPDoer
	TLS            *OTLPHTTPTLSConfig
	Protocol       string
	Transport      string
	ExportInterval time.Duration
	Timeout        time.Duration
	QueueSize      int
	// SpanAttributes are the configured `otel.span_attributes`, applied to every
	// exported span (Rust's build_tracer_provider).
	SpanAttributes map[string]string
	Now            func() time.Time
	// NewID overrides span/trace id generation (tests).
	NewID func(size int) string
}

// TracesClient records spans and exports them through an OTLP exporter.
type TracesClient struct {
	exporter       TracesExporter
	scope          OTLPScope
	environment    string
	serviceName    string
	serviceVersion string
	spanAttributes []MetricTagValue
	queueSize      int
	now            func() time.Time
	newID          func(size int) string

	mu       sync.Mutex
	queue    []OTLPSpan
	dropped  uint64
	interval time.Duration
	cancel   context.CancelFunc
	done     chan struct{}
	closed   bool
}

// NewTracesClient builds the client. A missing or unsupported exporter leaves
// it disabled.
func NewTracesClient(options TracesClientOptions) *TracesClient {
	client := &TracesClient{
		environment:    options.Environment,
		serviceName:    options.ServiceName,
		serviceVersion: options.ServiceVersion,
		spanAttributes: sortedMetricTags(options.SpanAttributes),
		queueSize:      options.QueueSize,
		now:            options.Now,
		newID:          options.NewID,
	}
	// Rust builds the tracer with the service name as its instrumentation scope
	// (provider.rs: `provider.tracer(settings.service_name)`), so every exported
	// span reports that scope.
	client.scope = OTLPScope{Name: client.tracerScopeName()}
	if client.now == nil {
		client.now = time.Now
	}
	if client.newID == nil {
		client.newID = randomHexID
	}
	if client.queueSize <= 0 {
		client.queueSize = DefaultTracesQueueSize
	}
	switch options.Transport {
	case TracesTransportGRPC:
		client.exporter = NewOTLPGRPCTracesExporter(OTLPGRPCTracesExporterOptions{
			Endpoint: options.Endpoint,
			Headers:  options.Headers,
			Timeout:  options.Timeout,
			TLS:      options.TLS,
		})
	default:
		client.exporter = NewOTLPTracesExporter(OTLPTracesExporterOptions{
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
		exportInterval = DefaultTracesExportInterval
	}
	if exportInterval > 0 {
		client.startExportLoop(exportInterval)
	}
	return client
}

// Enabled reports whether the client will export spans.
func (c *TracesClient) Enabled() bool {
	return c != nil && c.exporter != nil
}

// Tracer returns the client's tracer, or nil when tracing is disabled.
func (c *TracesClient) Tracer() *Tracer {
	if c == nil || !c.Enabled() {
		return nil
	}
	return &Tracer{client: c}
}

// emit queues one finished span, dropping it when the queue is full like the
// batch span processor's bounded queue.
func (c *TracesClient) emit(span OTLPSpan) {
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
	c.queue = append(c.queue, span)
}

// Dropped reports how many spans were dropped because the queue was full.
func (c *TracesClient) Dropped() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropped
}

// Flush exports the queued spans.
func (c *TracesClient) Flush(ctx context.Context) error {
	if c == nil || !c.Enabled() {
		return nil
	}
	request := c.snapshot()
	if len(request.ResourceSpans) == 0 {
		return nil
	}
	if err := c.exporter.Export(ctx, request); err != nil {
		logTracesExportFailure(err)
		return err
	}
	return nil
}

// Shutdown stops the export loop and flushes the remaining spans.
func (c *TracesClient) Shutdown(ctx context.Context) error {
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

// snapshot drains the queued spans into an OTLP batch.
func (c *TracesClient) snapshot() OTLPExportTracesRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.queue) == 0 {
		return OTLPExportTracesRequest{}
	}
	spans := append([]OTLPSpan(nil), c.queue...)
	c.queue = nil
	return OTLPExportTracesRequest{
		ResourceSpans: []OTLPResourceSpans{{
			Resource: OTLPResource{Attributes: c.resourceAttributes()},
			ScopeSpans: []OTLPScopeSpans{{
				Scope: c.scope,
				Spans: spans,
			}},
		}},
	}
}

func (c *TracesClient) startExportLoop(interval time.Duration) {
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

// resourceAttributes mirrors the metrics and logs pipelines' resource.
func (c *TracesClient) resourceAttributes() []MetricTagValue {
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

// Tracer starts spans for one client.
type Tracer struct {
	client *TracesClient
}

// StartSpan starts an internal span with no parent.
func (t *Tracer) StartSpan(name string, attributes map[string]string) *Span {
	return t.startSpan(nil, name, attributes, SpanKindInternal)
}

// StartSpanWithKind starts a span with an explicit kind, mirroring Rust's
// `otel.kind` span field (the app-server stamps `server` on request spans).
func (t *Tracer) StartSpanWithKind(name string, kind int, attributes map[string]string) *Span {
	return t.startSpan(nil, name, attributes, kind)
}

// StartSpanWithParent starts a span nested under the parent span, sharing its
// trace id.
func (t *Tracer) StartSpanWithParent(parent *Span, name string, attributes map[string]string) *Span {
	return t.startSpan(parent, name, attributes, SpanKindInternal)
}

// tracerScopeName reports the instrumentation scope Rust would use for this
// client: the configured service name, falling back to the Codex scope tag when
// a caller leaves it unset.
func (c *TracesClient) tracerScopeName() string {
	if c != nil && strings.TrimSpace(c.serviceName) != "" {
		return c.serviceName
	}
	return TracesScopeName
}

func (t *Tracer) startSpan(parent *Span, name string, attributes map[string]string, kind int) *Span {
	if t == nil || t.client == nil || !t.client.Enabled() {
		return nil
	}
	span := &Span{
		client:     t.client,
		Name:       name,
		Kind:       kind,
		TraceID:    t.client.newID(16),
		SpanID:     t.client.newID(8),
		startedAt:  t.client.now().UTC(),
		attributes: sortedMetricTags(attributes),
		// A root span is recorded; a span that continues a remote trace
		// inherits that trace's sampling decision instead (Rust's default
		// parent-based sampler).
		sampled: true,
	}
	if parent != nil {
		span.TraceID = parent.TraceID
		span.ParentSpanID = parent.SpanID
	}
	return span
}

// Span is one in-flight span. End exports it.
type Span struct {
	client       *TracesClient
	Name         string
	Kind         int
	TraceID      string
	SpanID       string
	ParentSpanID string
	// TraceState carries the W3C tracestate of the parent context; it is
	// exported with the span and merged with the configured entries when the
	// span propagates its own context.
	TraceState    string
	sampled       bool
	statusCode    int
	statusMessage string
	startedAt     time.Time
	endedAt       time.Time
	attributes    []MetricTagValue
	events        []OTLPSpanEvent
	mu            sync.Mutex
	ended         bool
}

// StartTime reports when the span started.
func (s *Span) StartTime() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.startedAt
}

// SetAttribute adds or replaces one span attribute.
func (s *Span) SetAttribute(key string, value string) {
	if s == nil || key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attributes = mergeMetricAttribute(s.attributes, key, value)
}

// SetStatus records the span status.
func (s *Span) SetStatus(code int, message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusCode = code
	s.statusMessage = message
}

// End finishes the span at the current time and exports it.
func (s *Span) End() {
	if s == nil {
		return
	}
	s.EndAt(time.Time{})
}

// EndAt finishes the span at the given time (the current client time when zero).
func (s *Span) EndAt(endedAt time.Time) {
	if s == nil || s.client == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	if endedAt.IsZero() {
		endedAt = s.client.now().UTC()
	}
	s.endedAt = endedAt.UTC()
	// A span that continues a non-sampled remote trace is not exported, like the
	// SDK's parent-based sampler.
	if !s.sampled {
		return
	}
	attributes := append([]MetricTagValue(nil), s.client.spanAttributes...)
	for _, attribute := range s.attributes {
		attributes = mergeMetricAttribute(attributes, attribute.Key, attribute.Value)
	}
	s.client.emit(OTLPSpan{
		TraceID:           s.TraceID,
		SpanID:            s.SpanID,
		ParentSpanID:      s.ParentSpanID,
		Name:              s.Name,
		Kind:              s.Kind,
		StartTimeUnixNano: spanTimeString(s.startedAt),
		EndTimeUnixNano:   spanTimeString(s.endedAt),
		Attributes:        attributes,
		StatusCode:        s.statusCode,
		StatusMessage:     s.statusMessage,
		TraceState:        s.TraceState,
		Events:            append([]OTLPSpanEvent(nil), s.events...),
	})
}

// AddEvent records one event on the span, the way a `tracing` event inside a
// span becomes an OTLP span event. Events on a non-recording span are dropped.
func (s *Span) AddEvent(name string, attributes map[string]string, at time.Time) {
	if s == nil || s.client == nil || !s.client.Enabled() || !s.sampled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	if at.IsZero() {
		at = s.client.now().UTC()
	}
	s.events = append(s.events, OTLPSpanEvent{
		Name:         name,
		TimeUnixNano: spanTimeString(at),
		Attributes:   sortedMetricTags(attributes),
	})
}

// sortedMetricTags converts a tag map into name-sorted OTLP attributes.
func sortedMetricTags(tags map[string]string) []MetricTagValue {
	if len(tags) == 0 {
		return nil
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]MetricTagValue, 0, len(keys))
	for _, key := range keys {
		out = append(out, MetricTagValue{Key: key, Value: tags[key]})
	}
	return out
}

// mergeMetricAttribute sets one attribute, replacing an existing key.
func mergeMetricAttribute(attributes []MetricTagValue, key string, value string) []MetricTagValue {
	for index := range attributes {
		if attributes[index].Key == key {
			attributes[index].Value = value
			return attributes
		}
	}
	return append(attributes, MetricTagValue{Key: key, Value: value})
}

// randomHexID returns size random bytes as hex.
func randomHexID(size int) string {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		// A random source failure must not break the caller: fall back to the
		// clock so the ids stay unique per call.
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))[:size*2]
	}
	return hex.EncodeToString(buffer)
}
