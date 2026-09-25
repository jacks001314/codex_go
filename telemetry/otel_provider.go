package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"

	"codex_go/network"
)

// Rust parity: codex-rs/otel/src/config.rs (OtelExporter / OtelSettings /
// resolve_exporter) and provider.rs::OtelProvider::try_new. Go builds the
// metrics, logging, and tracing pipelines; span producers are the remaining
// piece (Rust instruments its core with tracing spans).

// Otel exporter kind tags, mirroring codex-otel's OtelExporter variants.
const (
	OtelExporterNone     = "none"
	OtelExporterStatsig  = "statsig"
	OtelExporterOtlpHTTP = "otlp-http"
	OtelExporterOtlpGRPC = "otlp-grpc"
)

// OTLP HTTP protocol tags, mirroring codex-otel's OtelHttpProtocol.
const (
	OtelHTTPProtocolBinary = "binary"
	OtelHTTPProtocolJSON   = "json"
)

// OtelExporter mirrors codex-otel's resolved OtelExporter.
type OtelExporter struct {
	Kind     string
	Endpoint string
	Headers  map[string]string
	Protocol string
	TLS      *OTLPHTTPTLSConfig
}

// ResolveOtelExporter mirrors codex-otel's resolve_exporter: the built-in
// Statsig route resolves to its OTLP/HTTP JSON endpoint and API key, and only
// when the build opts into it (Rust's `cfg!(debug_assertions)` guard). Every
// other exporter is returned unchanged.
func ResolveOtelExporter(exporter OtelExporter) OtelExporter {
	if exporter.Kind != OtelExporterStatsig {
		return exporter
	}
	if !statsigMetricsRouteEnabled() {
		return OtelExporter{Kind: OtelExporterNone}
	}
	return OtelExporter{
		Kind:     OtelExporterOtlpHTTP,
		Endpoint: StatsigMetricsEndpoint,
		Headers:  map[string]string{StatsigMetricsAPIKeyHeader: StatsigMetricsAPIKey},
		Protocol: OtelHTTPProtocolJSON,
	}
}

// OtelSettings mirrors codex-otel's OtelSettings. Tracestate and span
// attributes are the sanitized config values; the tracing/log paths that would
// consume them are not implemented in Go.
type OtelSettings struct {
	Environment     string
	ServiceName     string
	ServiceVersion  string
	Exporter        OtelExporter
	TraceExporter   OtelExporter
	MetricsExporter OtelExporter
	RuntimeMetrics  bool
	SpanAttributes  map[string]string
	Tracestate      map[string]map[string]string
	// NetworkPolicy is the application policy the OTLP HTTP export clients
	// enforce (Rust #47408 makes managed HTTP exports cancellable).
	NetworkPolicy network.NetworkPolicy
}

// OtelProvider mirrors codex-otel's OtelProvider for the metrics, logging, and
// tracing pipelines.
type OtelProvider struct {
	metrics      *MetricsClient
	logs         *LogsClient
	traces       *TracesClient
	shutdownOnce sync.Once
}

// NewOtelProvider mirrors OtelProvider::try_new. It returns a nil provider
// without error when no supported exporter is enabled, which is what Rust does
// when every exporter is disabled.
func NewOtelProvider(settings OtelSettings) (*OtelProvider, error) {
	// Every OTLP HTTP export client built below enforces this generation.
	setOTLPExportPolicy(settings.NetworkPolicy)
	// Rust resolves the settings, validates the trace metadata before any
	// process-global OTEL state is installed, and only then builds the
	// pipelines (provider.rs::try_new).
	metricsExporter := ResolveOtelExporter(settings.MetricsExporter)
	logsExporter := ResolveOtelExporter(settings.Exporter)
	traceExporter := ResolveOtelExporter(settings.TraceExporter)
	if metricsExporter.Kind == OtelExporterNone && logsExporter.Kind == OtelExporterNone &&
		traceExporter.Kind == OtelExporterNone {
		// Tracestate propagation is process-global; clear it when these settings
		// do not install an active provider.
		if err := SetTracestateEntries(nil); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if traceExporter.Kind != OtelExporterNone {
		if err := ValidateSpanAttributes(settings.SpanAttributes); err != nil {
			return nil, err
		}
	}
	if err := ValidateTracestateEntries(settings.Tracestate); err != nil {
		return nil, err
	}

	var metrics *MetricsClient
	if metricsExporter.Kind != OtelExporterNone {
		if options, ok := metricsClientOptions(settings, metricsExporter); ok {
			if client := NewMetricsClient(options); client.Enabled() {
				metrics = client
			}
		}
	}
	// Rust's log pipeline uses the general `exporter` setting
	// (provider.rs::try_new -> build_logger(&settings.exporter)).
	var logs *LogsClient
	if logsExporter.Kind != OtelExporterNone {
		if options, ok := logsClientOptions(settings, logsExporter); ok {
			if client := NewLogsClient(options); client.Enabled() {
				logs = client
			}
		}
	}
	// Rust's trace pipeline uses the dedicated `trace_exporter` setting and
	// applies the configured span attributes to every exported span.
	var traces *TracesClient
	if traceExporter.Kind != OtelExporterNone {
		if options, ok := tracesClientOptions(settings, traceExporter); ok {
			if client := NewTracesClient(options); client.Enabled() {
				traces = client
			}
		}
	}
	if metrics == nil && logs == nil && traces == nil {
		return nil, nil
	}
	// The configured tracestate travels with every propagated trace context.
	if err := SetTracestateEntries(settings.Tracestate); err != nil {
		return nil, err
	}
	return &OtelProvider{metrics: metrics, logs: logs, traces: traces}, nil
}

// ValidateSpanAttributes mirrors codex-otel's validate_span_attributes: the
// configured per-span attributes must carry a key.
func ValidateSpanAttributes(attributes map[string]string) error {
	for key := range attributes {
		if key == "" {
			return errors.New("configured span attribute key must not be empty")
		}
	}
	return nil
}

// tracesClientOptions maps the resolved trace exporter onto the traces client,
// the way metricsClientOptions does for metrics.
func tracesClientOptions(settings OtelSettings, exporter OtelExporter) (TracesClientOptions, bool) {
	common := TracesClientOptions{
		Environment:    settings.Environment,
		ServiceName:    settings.ServiceName,
		ServiceVersion: settings.ServiceVersion,
		Endpoint:       exporter.Endpoint,
		Headers:        exporter.Headers,
		TLS:            exporter.TLS,
		SpanAttributes: settings.SpanAttributes,
	}
	switch exporter.Kind {
	case OtelExporterOtlpHTTP:
		switch exporter.Protocol {
		case "", OtelHTTPProtocolJSON, OtelHTTPProtocolBinary:
		default:
			slog.Warn("OTLP HTTP traces protocol is not supported", "protocol", exporter.Protocol)
			return TracesClientOptions{}, false
		}
		common.Protocol = exporter.Protocol
		common.Transport = TracesTransportHTTP
		return common, true
	case OtelExporterOtlpGRPC:
		common.Transport = TracesTransportGRPC
		return common, true
	default:
		return TracesClientOptions{}, false
	}
}

// logsClientOptions maps the resolved log exporter onto the log client, the way
// metricsClientOptions does for metrics.
func logsClientOptions(settings OtelSettings, exporter OtelExporter) (LogsClientOptions, bool) {
	common := LogsClientOptions{
		Environment:    settings.Environment,
		ServiceName:    settings.ServiceName,
		ServiceVersion: settings.ServiceVersion,
		Endpoint:       exporter.Endpoint,
		Headers:        exporter.Headers,
		TLS:            exporter.TLS,
	}
	switch exporter.Kind {
	case OtelExporterOtlpHTTP:
		switch exporter.Protocol {
		case "", OtelHTTPProtocolJSON, OtelHTTPProtocolBinary:
		default:
			slog.Warn("OTLP HTTP logs protocol is not supported", "protocol", exporter.Protocol)
			return LogsClientOptions{}, false
		}
		common.Protocol = exporter.Protocol
		common.Transport = LogsTransportHTTP
		return common, true
	case OtelExporterOtlpGRPC:
		common.Transport = LogsTransportGRPC
		return common, true
	default:
		return LogsClientOptions{}, false
	}
}

// metricsClientOptions maps the resolved metrics exporter onto the client.
func metricsClientOptions(settings OtelSettings, exporter OtelExporter) (MetricsClientOptions, bool) {
	common := MetricsClientOptions{
		Environment:    settings.Environment,
		ServiceName:    settings.ServiceName,
		ServiceVersion: settings.ServiceVersion,
		Endpoint:       exporter.Endpoint,
		Headers:        exporter.Headers,
		TLS:            exporter.TLS,
	}
	switch exporter.Kind {
	case OtelExporterOtlpHTTP:
		switch exporter.Protocol {
		case "", OtelHTTPProtocolJSON, OtelHTTPProtocolBinary:
		default:
			slog.Warn("OTLP HTTP metrics protocol is not supported", "protocol", exporter.Protocol)
			return MetricsClientOptions{}, false
		}
		common.Protocol = exporter.Protocol
		common.Transport = MetricsTransportHTTP
		return common, true
	case OtelExporterOtlpGRPC:
		common.Transport = MetricsTransportGRPC
		return common, true
	default:
		return MetricsClientOptions{}, false
	}
}

// Metrics returns the provider's metrics client, or nil when metrics are
// disabled.
func (p *OtelProvider) Metrics() *MetricsClient {
	if p == nil {
		return nil
	}
	return p.metrics
}

// Tracer returns the provider's tracer, or nil when the tracing pipeline is
// disabled.
func (p *OtelProvider) Tracer() *Tracer {
	if p == nil {
		return nil
	}
	return p.traces.Tracer()
}

// Traces returns the provider's traces client, or nil when tracing is disabled.
func (p *OtelProvider) Traces() *TracesClient {
	if p == nil {
		return nil
	}
	return p.traces
}

// Logs returns the provider's log client, or nil when the logging pipeline is
// disabled.
func (p *OtelProvider) Logs() *LogsClient {
	if p == nil {
		return nil
	}
	return p.logs
}

// LogsHandler returns a slog handler that forwards exported records to the
// provider's log client, wrapping next, or nil when the logging pipeline is
// disabled (Rust installs its log layer only for an enabled provider).
func (p *OtelProvider) LogsHandler(next slog.Handler) *LogsSlogHandler {
	if p == nil || p.logs == nil || !p.logs.Enabled() {
		return nil
	}
	return NewLogsSlogHandler(p.logs, next)
}

// Shutdown flushes and stops the exporters at most once.
func (p *OtelProvider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var err error
	p.shutdownOnce.Do(func() {
		// Rust shuts the tracer provider down first, then metrics, then the
		// logger (provider.rs::shutdown).
		if tracesErr := p.traces.Shutdown(ctx); tracesErr != nil {
			err = tracesErr
		}
		if metricsErr := p.metrics.Shutdown(ctx); metricsErr != nil && err == nil {
			err = metricsErr
		}
		if logsErr := p.logs.Shutdown(ctx); logsErr != nil && err == nil {
			err = logsErr
		}
	})
	return err
}

// processStartRecorded mirrors process.rs's PROCESS_START_RECORDED.
var processStartRecorded atomic.Bool

// resetProcessStartLatch restores the process-start latch between tests.
func resetProcessStartLatch() {
	processStartRecorded.Store(false)
}

// RecordProcessStartOnce mirrors codex-otel's record_process_start_once: record
// the process-start counter at most once per process, tagged with the bounded
// originator.
func RecordProcessStartOnce(metrics *MetricsClient, originator string) bool {
	if metrics == nil || !metrics.Enabled() {
		return false
	}
	if !processStartRecorded.CompareAndSwap(false, true) {
		return false
	}
	metrics.Counter(ProcessStartMetric, 1, map[string]string{
		OriginatorTag: BoundedOriginatorTagValue(originator),
	})
	return true
}
