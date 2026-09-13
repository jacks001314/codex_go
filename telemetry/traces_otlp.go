package telemetry

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// Rust parity: codex-rs/otel/src/provider.rs::build_tracer_provider and the OTLP
// span exporters behind it. The transports mirror the metrics and logs
// pipelines (metrics_otlp.go / logs_otlp.go).

// TracesExporterTimeoutEnv / TracesExporterSignalTimeoutEnv mirror the OTLP
// timeout environment variables Rust resolves per signal.
const (
	TracesExporterTimeoutEnv       = "OTEL_EXPORTER_OTLP_TIMEOUT"
	TracesExporterSignalTimeoutEnv = "OTEL_EXPORTER_OTLP_TRACES_TIMEOUT"
	// DefaultTracesExportTimeout mirrors opentelemetry-otlp's default.
	DefaultTracesExportTimeout = 10 * time.Second
	// DefaultTracesExportInterval mirrors the batch span processor's default.
	DefaultTracesExportInterval = 5 * time.Second
	// TracesTransportHTTP and TracesTransportGRPC select the OTLP transport.
	TracesTransportHTTP = "http"
	TracesTransportGRPC = "grpc"
	// TracesScopeName is the instrumentation scope reported for Codex spans
	// when the tracing pipeline has no configured service name. Rust names the
	// tracer after the service name, so that name wins in every real setup.
	TracesScopeName = "codex"
)

// Span kinds (opentelemetry-proto SpanKind).
const (
	SpanKindInternal = 1
	SpanKindServer   = 2
	SpanKindClient   = 3
)

// Span status codes (opentelemetry-proto StatusCode).
const (
	SpanStatusUnset = 0
	SpanStatusOK    = 1
	SpanStatusError = 2
)

// OTLPExportTracesRequest is one encoded span batch.
type OTLPExportTracesRequest struct {
	ResourceSpans []OTLPResourceSpans
}

// OTLPResourceSpans groups the resource and scopes of one batch.
type OTLPResourceSpans struct {
	Resource   OTLPResource
	ScopeSpans []OTLPScopeSpans
}

// OTLPScopeSpans groups one scope's spans.
type OTLPScopeSpans struct {
	Scope OTLPScope
	Spans []OTLPSpan
}

// OTLPSpan is one exported span.
type OTLPSpan struct {
	TraceID           string
	SpanID            string
	ParentSpanID      string
	Name              string
	Kind              int
	StartTimeUnixNano string
	EndTimeUnixNano   string
	Attributes        []MetricTagValue
	StatusCode        int
	StatusMessage     string
	// TraceState is the W3C tracestate carried with the span, exported the way
	// the OTLP span schema reports it.
	TraceState string
	// Events are the span events recorded while the span was open (Rust's
	// trace-safe events become OTLP span events).
	Events []OTLPSpanEvent
}

// OTLPSpanEvent is one event attached to an exported span.
type OTLPSpanEvent struct {
	Name         string
	TimeUnixNano string
	Attributes   []MetricTagValue
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	TraceState        string          `json:"traceState,omitempty"`
	ParentSpanID      string          `json:"parentSpanId,omitempty"`
	Name              string          `json:"name"`
	Kind              int             `json:"kind,omitempty"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []otlpAttribute `json:"attributes,omitempty"`
	Events            []otlpSpanEvent `json:"events,omitempty"`
	Status            *otlpStatus     `json:"status,omitempty"`
}

type otlpSpanEvent struct {
	TimeUnixNano string          `json:"timeUnixNano"`
	Name         string          `json:"name"`
	Attributes   []otlpAttribute `json:"attributes,omitempty"`
}

type otlpStatus struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// MarshalJSON encodes the request into the OTLP JSON shape.
func (r OTLPExportTracesRequest) MarshalJSON() ([]byte, error) {
	resourceSpans := make([]otlpResourceSpans, 0, len(r.ResourceSpans))
	for _, resource := range r.ResourceSpans {
		encodedResource := otlpResource{Attributes: otlpAttributes(resource.Resource.Attributes)}
		scopeSpans := make([]otlpScopeSpans, 0, len(resource.ScopeSpans))
		for _, scoped := range resource.ScopeSpans {
			spans := make([]otlpSpan, 0, len(scoped.Spans))
			for _, span := range scoped.Spans {
				encoded := otlpSpan{
					TraceID:           span.TraceID,
					SpanID:            span.SpanID,
					TraceState:        span.TraceState,
					ParentSpanID:      span.ParentSpanID,
					Name:              span.Name,
					Kind:              span.Kind,
					StartTimeUnixNano: span.StartTimeUnixNano,
					EndTimeUnixNano:   span.EndTimeUnixNano,
					Attributes:        otlpAttributes(span.Attributes),
				}
				if span.StatusCode != SpanStatusUnset || span.StatusMessage != "" {
					encoded.Status = &otlpStatus{Code: span.StatusCode, Message: span.StatusMessage}
				}
				if len(span.Events) > 0 {
					encoded.Events = make([]otlpSpanEvent, 0, len(span.Events))
					for _, event := range span.Events {
						encoded.Events = append(encoded.Events, otlpSpanEvent{
							TimeUnixNano: event.TimeUnixNano,
							Name:         event.Name,
							Attributes:   otlpAttributes(event.Attributes),
						})
					}
				}
				spans = append(spans, encoded)
			}
			scopeSpans = append(scopeSpans, otlpScopeSpans{
				Scope: otlpScope{Name: scoped.Scope.Name, Version: scoped.Scope.Version},
				Spans: spans,
			})
		}
		resourceSpans = append(resourceSpans, otlpResourceSpans{
			Resource:   encodedResource,
			ScopeSpans: scopeSpans,
		})
	}
	return json.Marshal(struct {
		ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
	}{ResourceSpans: resourceSpans})
}

// protoRequest converts the batch into the OTLP protobuf message used by the
// binary and gRPC transports, built from the official OTLP trace definitions.
func (r OTLPExportTracesRequest) protoRequest() (*collectortracepb.ExportTraceServiceRequest, error) {
	request := &collectortracepb.ExportTraceServiceRequest{}
	for _, resource := range r.ResourceSpans {
		scopeSpans := make([]*tracepb.ScopeSpans, 0, len(resource.ScopeSpans))
		for _, scoped := range resource.ScopeSpans {
			spans := make([]*tracepb.Span, 0, len(scoped.Spans))
			for _, span := range scoped.Spans {
				startTime, err := parseUnixNano(span.StartTimeUnixNano)
				if err != nil {
					return nil, err
				}
				endTime, err := parseUnixNano(span.EndTimeUnixNano)
				if err != nil {
					return nil, err
				}
				traceID, err := hex.DecodeString(span.TraceID)
				if err != nil || len(traceID) != 16 {
					return nil, fmt.Errorf("OTLP span trace id %q must be 16 bytes of hex", span.TraceID)
				}
				spanID, err := hex.DecodeString(span.SpanID)
				if err != nil || len(spanID) != 8 {
					return nil, fmt.Errorf("OTLP span id %q must be 8 bytes of hex", span.SpanID)
				}
				encoded := &tracepb.Span{
					TraceId:           traceID,
					SpanId:            spanID,
					TraceState:        span.TraceState,
					Name:              span.Name,
					Kind:              tracepb.Span_SpanKind(span.Kind),
					StartTimeUnixNano: startTime,
					EndTimeUnixNano:   endTime,
					Attributes:        protoAttributes(span.Attributes),
					Status:            &tracepb.Status{Code: tracepb.Status_StatusCode(span.StatusCode), Message: span.StatusMessage},
				}
				if span.ParentSpanID != "" {
					parentSpanID, err := hex.DecodeString(span.ParentSpanID)
					if err != nil || len(parentSpanID) != 8 {
						return nil, fmt.Errorf("OTLP parent span id %q must be 8 bytes of hex", span.ParentSpanID)
					}
					encoded.ParentSpanId = parentSpanID
				}
				for _, event := range span.Events {
					eventTime, err := parseUnixNano(event.TimeUnixNano)
					if err != nil {
						return nil, err
					}
					encoded.Events = append(encoded.Events, &tracepb.Span_Event{
						TimeUnixNano: eventTime,
						Name:         event.Name,
						Attributes:   protoAttributes(event.Attributes),
					})
				}
				spans = append(spans, encoded)
			}
			scopeSpans = append(scopeSpans, &tracepb.ScopeSpans{
				Scope: &commonpb.InstrumentationScope{Name: scoped.Scope.Name, Version: scoped.Scope.Version},
				Spans: spans,
			})
		}
		request.ResourceSpans = append(request.ResourceSpans, &tracepb.ResourceSpans{
			Resource:   &resourcepb.Resource{Attributes: protoAttributes(resource.Resource.Attributes)},
			ScopeSpans: scopeSpans,
		})
	}
	return request, nil
}

// TracesExporter posts one encoded span batch to the configured OTLP endpoint.
type TracesExporter interface {
	Export(ctx context.Context, request OTLPExportTracesRequest) error
	Close() error
}

// OTLPTracesExporterOptions configures the OTLP/HTTP span transport.
type OTLPTracesExporterOptions struct {
	Endpoint   string
	Headers    map[string]string
	HTTPClient HTTPDoer
	Timeout    time.Duration
	TLS        *OTLPHTTPTLSConfig
	Protocol   string
}

// OTLPTracesExporter posts encoded span batches to one OTLP/HTTP endpoint.
type OTLPTracesExporter struct {
	endpoint     string
	headers      map[string]string
	httpClient   HTTPDoer
	timeout      time.Duration
	requireHTTPS bool
	protocol     string
}

// NewOTLPTracesExporter builds the OTLP/HTTP span exporter.
func NewOTLPTracesExporter(options OTLPTracesExporterOptions) *OTLPTracesExporter {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		return nil
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = resolveTracesExportTimeout()
	}
	client := options.HTTPClient
	var requireHTTPS bool
	if client == nil {
		var err error
		client, requireHTTPS, err = buildOTLPHTTPClient(options.TLS, timeout)
		if err != nil {
			logTracesExportFailure(err)
			return nil
		}
	}
	return &OTLPTracesExporter{
		endpoint:     endpoint,
		headers:      cloneMetricHeaderMap(options.Headers),
		httpClient:   client,
		timeout:      timeout,
		requireHTTPS: requireHTTPS,
		protocol:     options.Protocol,
	}
}

// Close releases the HTTP transport; the shared client owns nothing to close.
func (e *OTLPTracesExporter) Close() error {
	return nil
}

// Export posts one encoded span batch.
func (e *OTLPTracesExporter) Export(ctx context.Context, request OTLPExportTracesRequest) error {
	if e == nil {
		return nil
	}
	if len(request.ResourceSpans) == 0 {
		return nil
	}
	var payload []byte
	contentType := "application/json"
	if e.protocol == OtelHTTPProtocolBinary {
		encoded, err := request.protoRequest()
		if err != nil {
			return err
		}
		payload, err = proto.Marshal(encoded)
		if err != nil {
			return err
		}
		contentType = "application/x-protobuf"
	} else {
		var err error
		payload, err = request.MarshalJSON()
		if err != nil {
			return err
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if e.requireHTTPS && !strings.HasPrefix(e.endpoint, "https://") {
		return fmt.Errorf("OTLP traces endpoint %q must use HTTPS for mTLS", e.endpoint)
	}
	requestCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, e.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpRequest.Header.Set("Content-Type", contentType)
	for key, value := range e.headers {
		httpRequest.Header.Set(key, value)
	}
	response, err := e.httpClient.Do(httpRequest)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OTLP traces export returned HTTP %d", response.StatusCode)
	}
	return nil
}

// OTLPGRPCTracesExporterOptions configures the OTLP gRPC span transport.
type OTLPGRPCTracesExporterOptions struct {
	Endpoint string
	Headers  map[string]string
	Timeout  time.Duration
	TLS      *OTLPHTTPTLSConfig
}

// OTLPGRPCTracesExporter posts encoded span batches over OTLP gRPC.
type OTLPGRPCTracesExporter struct {
	client  collectortracepb.TraceServiceClient
	conn    *grpc.ClientConn
	headers map[string]string
	timeout time.Duration
}

// NewOTLPGRPCTracesExporter builds the gRPC span exporter.
func NewOTLPGRPCTracesExporter(options OTLPGRPCTracesExporterOptions) *OTLPGRPCTracesExporter {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		logTracesExportFailure(fmt.Errorf("invalid OTLP gRPC endpoint %q", endpoint))
		return nil
	}
	credentialsOption, err := grpcCredentials(parsed, options.TLS)
	if err != nil {
		logTracesExportFailure(err)
		return nil
	}
	conn, err := grpc.NewClient(parsed.Host, credentialsOption)
	if err != nil {
		logTracesExportFailure(err)
		return nil
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = resolveTracesExportTimeout()
	}
	return &OTLPGRPCTracesExporter{
		client:  collectortracepb.NewTraceServiceClient(conn),
		conn:    conn,
		headers: cloneMetricHeaderMap(options.Headers),
		timeout: timeout,
	}
}

// Export posts one encoded batch over gRPC.
func (e *OTLPGRPCTracesExporter) Export(ctx context.Context, request OTLPExportTracesRequest) error {
	if e == nil || e.client == nil {
		return nil
	}
	if len(request.ResourceSpans) == 0 {
		return nil
	}
	encoded, err := request.protoRequest()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx := ctx
	if e.timeout > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}
	if len(e.headers) > 0 {
		requestCtx = metadata.NewOutgoingContext(requestCtx, metadata.New(e.headers))
	}
	_, err = e.client.Export(requestCtx, encoded)
	return err
}

// Close releases the gRPC connection.
func (e *OTLPGRPCTracesExporter) Close() error {
	if e == nil || e.conn == nil {
		return nil
	}
	return e.conn.Close()
}

// resolveTracesExportTimeout mirrors otlp::resolve_otlp_timeout for traces.
func resolveTracesExportTimeout() time.Duration {
	if timeout, ok := readMetricsTimeoutEnv(TracesExporterSignalTimeoutEnv); ok {
		return timeout
	}
	if timeout, ok := readMetricsTimeoutEnv(TracesExporterTimeoutEnv); ok {
		return timeout
	}
	return DefaultTracesExportTimeout
}

// logTracesExportFailure reports a transport problem without failing startup.
func logTracesExportFailure(err error) {
	if err == nil {
		return
	}
	slog.Warn("OTLP trace export is disabled", "error", err.Error())
}

// IsTraceExportTarget mirrors codex-otel's trace_export_filter for spans: h2
// spawns explicit-root spans that escape the SDK's telemetry suppression, so
// exporting them would make the OTLP transport generate more exports.
func IsTraceExportTarget(target string) bool {
	target = strings.TrimSpace(target)
	return target != "h2" && !strings.HasPrefix(target, "h2::")
}

// spanTimeString formats a span timestamp the way the metrics pipeline does.
func spanTimeString(value time.Time) string {
	if value.IsZero() {
		value = time.Now()
	}
	return strconv.FormatInt(value.UnixNano(), 10)
}
