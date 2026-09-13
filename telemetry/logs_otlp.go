package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// Rust parity: codex-rs/otel/src/provider.rs::build_logger and the OTLP log
// exporters behind it. The transport mirrors the metrics pipeline
// (metrics_otlp.go / metrics_grpc.go): the same OTLP/HTTP JSON, OTLP/HTTP
// binary, and OTLP gRPC transports, with the same TLS and header handling.

// LogsExporterTimeoutEnv / LogsExporterSignalTimeoutEnv mirror the OTLP timeout
// environment variables Rust resolves per signal.
const (
	LogsExporterTimeoutEnv       = "OTEL_EXPORTER_OTLP_TIMEOUT"
	LogsExporterSignalTimeoutEnv = "OTEL_EXPORTER_OTLP_LOGS_TIMEOUT"
	// DefaultLogsExportTimeout mirrors opentelemetry-otlp's default.
	DefaultLogsExportTimeout = 10 * time.Second
	// DefaultLogsExportInterval mirrors the batch log processor's default.
	DefaultLogsExportInterval = 5 * time.Second
	// LogsTransportHTTP and LogsTransportGRPC select the OTLP transport.
	LogsTransportHTTP = "http"
	LogsTransportGRPC = "grpc"
	// LogsScopeName is the instrumentation scope reported for Codex log records.
	LogsScopeName = "codex"
)

// OTLP severity numbers (opentelemetry-proto SeverityNumber).
const (
	otlpSeverityDebug = 5
	otlpSeverityInfo  = 9
	otlpSeverityWarn  = 13
	otlpSeverityError = 17
)

// OTLPExportLogsRequest is one encoded log batch.
type OTLPExportLogsRequest struct {
	ResourceLogs []OTLPResourceLogs
}

// OTLPResourceLogs groups the resource and scopes of one batch.
type OTLPResourceLogs struct {
	Resource  OTLPResource
	ScopeLogs []OTLPScopeLogs
}

// OTLPScopeLogs groups one scope's log records.
type OTLPScopeLogs struct {
	Scope      OTLPScope
	LogRecords []OTLPLogRecord
}

// OTLPLogRecord is one exported log record.
type OTLPLogRecord struct {
	TimeUnixNano   string
	SeverityNumber int
	SeverityText   string
	Body           string
	Attributes     []MetricTagValue
	// Target is the record's tracing target, which OpenTelemetry reports as the
	// instrumentation scope name (the appender bridge groups records by target).
	Target string
}

// otlpResourceLogs is the OTLP JSON shape (int64 fields are strings).
type otlpResourceLogs struct {
	Resource  otlpResource    `json:"resource"`
	ScopeLogs []otlpScopeLogs `json:"scopeLogs"`
}

type otlpScopeLogs struct {
	Scope      otlpScope       `json:"scope"`
	LogRecords []otlpLogRecord `json:"logRecords"`
}

type otlpLogRecord struct {
	TimeUnixNano   string          `json:"timeUnixNano,omitempty"`
	SeverityNumber int             `json:"severityNumber,omitempty"`
	SeverityText   string          `json:"severityText,omitempty"`
	Body           *otlpLogBody    `json:"body,omitempty"`
	Attributes     []otlpAttribute `json:"attributes,omitempty"`
}

type otlpLogBody struct {
	StringValue string `json:"stringValue"`
}

// MarshalJSON encodes the request into the OTLP JSON shape.
func (r OTLPExportLogsRequest) MarshalJSON() ([]byte, error) {
	resourceLogs := make([]otlpResourceLogs, 0, len(r.ResourceLogs))
	for _, resource := range r.ResourceLogs {
		encodedResource := otlpResource{Attributes: otlpAttributes(resource.Resource.Attributes)}
		scopeLogs := make([]otlpScopeLogs, 0, len(resource.ScopeLogs))
		for _, scoped := range resource.ScopeLogs {
			records := make([]otlpLogRecord, 0, len(scoped.LogRecords))
			for _, record := range scoped.LogRecords {
				encoded := otlpLogRecord{
					TimeUnixNano:   record.TimeUnixNano,
					SeverityNumber: record.SeverityNumber,
					SeverityText:   record.SeverityText,
					Attributes:     otlpAttributes(record.Attributes),
				}
				// Rust's event records carry no `message` field, so their body is
				// absent rather than an empty string.
				if record.Body != "" {
					encoded.Body = &otlpLogBody{StringValue: record.Body}
				}
				records = append(records, encoded)
			}
			scopeLogs = append(scopeLogs, otlpScopeLogs{
				Scope:      otlpScope{Name: scoped.Scope.Name, Version: scoped.Scope.Version},
				LogRecords: records,
			})
		}
		resourceLogs = append(resourceLogs, otlpResourceLogs{
			Resource:  encodedResource,
			ScopeLogs: scopeLogs,
		})
	}
	return json.Marshal(struct {
		ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
	}{ResourceLogs: resourceLogs})
}

// protoRequest converts the batch into the OTLP protobuf message used by the
// binary and gRPC transports, built from the official OTLP log definitions.
func (r OTLPExportLogsRequest) protoRequest() (*collectorlogspb.ExportLogsServiceRequest, error) {
	request := &collectorlogspb.ExportLogsServiceRequest{}
	for _, resource := range r.ResourceLogs {
		scopeLogs := make([]*logspb.ScopeLogs, 0, len(resource.ScopeLogs))
		for _, scoped := range resource.ScopeLogs {
			records := make([]*logspb.LogRecord, 0, len(scoped.LogRecords))
			for _, record := range scoped.LogRecords {
				timeNano, err := parseUnixNano(record.TimeUnixNano)
				if err != nil {
					return nil, err
				}
				encoded := &logspb.LogRecord{
					TimeUnixNano:   timeNano,
					SeverityNumber: logspb.SeverityNumber(record.SeverityNumber),
					SeverityText:   record.SeverityText,
					Attributes:     protoAttributes(record.Attributes),
				}
				if record.Body != "" {
					encoded.Body = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: record.Body}}
				}
				records = append(records, encoded)
			}
			scopeLogs = append(scopeLogs, &logspb.ScopeLogs{
				Scope:      &commonpb.InstrumentationScope{Name: scoped.Scope.Name, Version: scoped.Scope.Version},
				LogRecords: records,
			})
		}
		request.ResourceLogs = append(request.ResourceLogs, &logspb.ResourceLogs{
			Resource:  &resourcepb.Resource{Attributes: protoAttributes(resource.Resource.Attributes)},
			ScopeLogs: scopeLogs,
		})
	}
	return request, nil
}

// LogsExporter posts one encoded log batch to the configured OTLP endpoint. It
// is implemented by the OTLP/HTTP and OTLP/gRPC transports.
type LogsExporter interface {
	Export(ctx context.Context, request OTLPExportLogsRequest) error
	Close() error
}

// OTLPLogsExporterOptions configures the OTLP/HTTP log transport. An empty
// Endpoint disables the exporter.
type OTLPLogsExporterOptions struct {
	Endpoint   string
	Headers    map[string]string
	HTTPClient HTTPDoer
	Timeout    time.Duration
	TLS        *OTLPHTTPTLSConfig
	// Protocol selects the OTLP/HTTP payload encoding: OtelHTTPProtocolJSON
	// (the default) or OtelHTTPProtocolBinary.
	Protocol string
}

// OTLPLogsExporter posts encoded log batches to one OTLP/HTTP endpoint.
type OTLPLogsExporter struct {
	endpoint     string
	headers      map[string]string
	httpClient   HTTPDoer
	timeout      time.Duration
	requireHTTPS bool
	protocol     string
}

// NewOTLPLogsExporter builds the OTLP/HTTP log exporter.
func NewOTLPLogsExporter(options OTLPLogsExporterOptions) *OTLPLogsExporter {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		return nil
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = resolveLogsExportTimeout()
	}
	client := options.HTTPClient
	var requireHTTPS bool
	if client == nil {
		var err error
		client, requireHTTPS, err = buildOTLPHTTPClient(options.TLS, timeout)
		if err != nil {
			logLogsExportFailure(err)
			return nil
		}
	}
	return &OTLPLogsExporter{
		endpoint:     endpoint,
		headers:      cloneMetricHeaderMap(options.Headers),
		httpClient:   client,
		timeout:      timeout,
		requireHTTPS: requireHTTPS,
		protocol:     options.Protocol,
	}
}

// Close releases the HTTP transport; the shared client owns nothing to close.
func (e *OTLPLogsExporter) Close() error {
	return nil
}

// Export posts one encoded log batch.
func (e *OTLPLogsExporter) Export(ctx context.Context, request OTLPExportLogsRequest) error {
	if e == nil {
		return nil
	}
	if len(request.ResourceLogs) == 0 {
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
		return fmt.Errorf("OTLP logs endpoint %q must use HTTPS for mTLS", e.endpoint)
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
		return fmt.Errorf("OTLP logs export returned HTTP %d", response.StatusCode)
	}
	return nil
}

// OTLPGRPCLogsExporterOptions configures the OTLP gRPC log transport.
type OTLPGRPCLogsExporterOptions struct {
	Endpoint string
	Headers  map[string]string
	Timeout  time.Duration
	TLS      *OTLPHTTPTLSConfig
}

// OTLPGRPCLogsExporter posts encoded log batches over OTLP gRPC.
type OTLPGRPCLogsExporter struct {
	client  collectorlogspb.LogsServiceClient
	conn    *grpc.ClientConn
	headers map[string]string
	timeout time.Duration
}

// NewOTLPGRPCLogsExporter builds the gRPC log exporter. It returns nil when the
// endpoint is empty or the TLS settings cannot be built, mirroring the metrics
// gRPC exporter.
func NewOTLPGRPCLogsExporter(options OTLPGRPCLogsExporterOptions) *OTLPGRPCLogsExporter {
	endpoint := strings.TrimSpace(options.Endpoint)
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		logLogsExportFailure(fmt.Errorf("invalid OTLP gRPC endpoint %q", endpoint))
		return nil
	}
	credentialsOption, err := grpcCredentials(parsed, options.TLS)
	if err != nil {
		logLogsExportFailure(err)
		return nil
	}
	conn, err := grpc.NewClient(parsed.Host, credentialsOption)
	if err != nil {
		logLogsExportFailure(err)
		return nil
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = resolveLogsExportTimeout()
	}
	return &OTLPGRPCLogsExporter{
		client:  collectorlogspb.NewLogsServiceClient(conn),
		conn:    conn,
		headers: cloneMetricHeaderMap(options.Headers),
		timeout: timeout,
	}
}

// Export posts one encoded batch over gRPC.
func (e *OTLPGRPCLogsExporter) Export(ctx context.Context, request OTLPExportLogsRequest) error {
	if e == nil || e.client == nil {
		return nil
	}
	if len(request.ResourceLogs) == 0 {
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
func (e *OTLPGRPCLogsExporter) Close() error {
	if e == nil || e.conn == nil {
		return nil
	}
	return e.conn.Close()
}

// resolveLogsExportTimeout mirrors otlp::resolve_otlp_timeout for the logs
// signal: the signal-specific variable wins over the generic one.
func resolveLogsExportTimeout() time.Duration {
	if timeout, ok := readMetricsTimeoutEnv(LogsExporterSignalTimeoutEnv); ok {
		return timeout
	}
	if timeout, ok := readMetricsTimeoutEnv(LogsExporterTimeoutEnv); ok {
		return timeout
	}
	return DefaultLogsExportTimeout
}

// logLogsExportFailure reports a transport problem without failing startup.
func logLogsExportFailure(err error) {
	if err == nil {
		return
	}
	slog.Warn("OTLP log export is disabled", "error", err.Error())
}

// severityForLevel maps a slog level onto the OTLP severity number and text
// (Rust's tracing levels map onto the same OTLP severity scale).
func severityForLevel(level slog.Level) (int, string) {
	switch {
	case level >= slog.LevelError:
		return otlpSeverityError, "ERROR"
	case level >= slog.LevelWarn:
		return otlpSeverityWarn, "WARN"
	case level >= slog.LevelInfo:
		return otlpSeverityInfo, "INFO"
	default:
		return otlpSeverityDebug, "DEBUG"
	}
}

// timeUnixNanoString formats a record time the way the metrics pipeline does.
func timeUnixNanoString(value time.Time) string {
	if value.IsZero() {
		value = time.Now()
	}
	return strconv.FormatInt(value.UnixNano(), 10)
}
