package telemetry

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// Rust parity: codex-rs/otel/src/config.rs (OtelExporter / OtelSettings /
// resolve_exporter) and provider.rs::OtelProvider::try_new. Go supports the
// metrics pipeline only: it has no OTLP tracing or logging provider, so the
// log/trace exporter settings are carried but never built. A metrics-only
// provider is returned when the resolved metrics exporter is enabled; traces or
// logs alone would leave nothing to build (Rust returns a provider for them).

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
}

// OtelProvider mirrors codex-otel's OtelProvider for the metrics pipeline.
type OtelProvider struct {
	metrics      *MetricsClient
	shutdownOnce sync.Once
}

// NewOtelProvider mirrors OtelProvider::try_new for the metrics pipeline. It
// returns a nil provider without error when no supported exporter is enabled,
// which is what Rust does when every exporter is disabled.
func NewOtelProvider(settings OtelSettings) (*OtelProvider, error) {
	metricsExporter := ResolveOtelExporter(settings.MetricsExporter)
	if metricsExporter.Kind == OtelExporterNone {
		return nil, nil
	}
	options, ok := metricsClientOptions(settings, metricsExporter)
	if !ok {
		return nil, nil
	}
	client := NewMetricsClient(options)
	if !client.Enabled() {
		return nil, nil
	}
	return &OtelProvider{metrics: client}, nil
}

// metricsClientOptions maps the resolved metrics exporter onto the client. The
// Go exporter speaks OTLP/HTTP (JSON or binary protobuf); the gRPC transport
// Rust supports disables export here and is reported instead.
func metricsClientOptions(settings OtelSettings, exporter OtelExporter) (MetricsClientOptions, bool) {
	switch exporter.Kind {
	case OtelExporterOtlpHTTP:
		switch exporter.Protocol {
		case "", OtelHTTPProtocolJSON, OtelHTTPProtocolBinary:
		default:
			slog.Warn("OTLP HTTP metrics protocol is not supported", "protocol", exporter.Protocol)
			return MetricsClientOptions{}, false
		}
		return MetricsClientOptions{
			Environment:    settings.Environment,
			ServiceName:    settings.ServiceName,
			ServiceVersion: settings.ServiceVersion,
			Endpoint:       exporter.Endpoint,
			Headers:        exporter.Headers,
			TLS:            exporter.TLS,
			Protocol:       exporter.Protocol,
		}, true
	case OtelExporterOtlpGRPC:
		slog.Warn("OTLP gRPC metrics export is not supported", "endpoint", exporter.Endpoint)
		return MetricsClientOptions{}, false
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

// Shutdown flushes and stops the metrics exporter at most once.
func (p *OtelProvider) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	var err error
	p.shutdownOnce.Do(func() {
		err = p.metrics.Shutdown(ctx)
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
