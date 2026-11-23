package telemetry

import (
	"context"
	"strings"
	"testing"
)

// Rust #62854: the specialized constructors reject a configuration that asks
// for a signal or transport they cannot build.
func TestHTTPOnlyProviderConstructorsRejectUnsupportedSignalsLikeRust(t *testing.T) {
	logs := OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: "https://logs.test/v1/logs"}
	metrics := OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: "https://metrics.test/v1/metrics"}
	traces := OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: "https://traces.test/v1/traces"}
	grpcTraces := OtelExporter{Kind: OtelExporterOtlpGRPC, Endpoint: "https://traces.test:4317"}
	grpcMetrics := OtelExporter{Kind: OtelExporterOtlpGRPC, Endpoint: "https://metrics.test:4317"}
	const unsupported = "configured exporter is not supported by this provider"
	for _, testCase := range []struct {
		name     string
		build    func(OtelSettings) (*OtelProvider, error)
		settings OtelSettings
		want     string
	}{
		{name: "traces rejects logs", build: NewHTTPTracesOtelProvider, settings: OtelSettings{Exporter: logs, TraceExporter: traces}, want: unsupported},
		{name: "traces rejects metrics", build: NewHTTPTracesOtelProvider, settings: OtelSettings{TraceExporter: traces, MetricsExporter: metrics}, want: unsupported},
		{name: "traces rejects statsig metrics", build: NewHTTPTracesOtelProvider, settings: OtelSettings{TraceExporter: traces, MetricsExporter: OtelExporter{Kind: OtelExporterStatsig}}, want: unsupported},
		{name: "traces rejects grpc", build: NewHTTPTracesOtelProvider, settings: OtelSettings{TraceExporter: grpcTraces}, want: "this provider requires OTLP/HTTP for traces"},
		{name: "metrics rejects logs", build: NewHTTPMetricsOtelProvider, settings: OtelSettings{Exporter: logs, MetricsExporter: metrics}, want: unsupported},
		{name: "metrics rejects traces", build: NewHTTPMetricsOtelProvider, settings: OtelSettings{MetricsExporter: metrics, TraceExporter: traces}, want: unsupported},
		{name: "metrics rejects grpc", build: NewHTTPMetricsOtelProvider, settings: OtelSettings{MetricsExporter: grpcMetrics}, want: "this provider requires OTLP/HTTP for metrics"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			provider, err := testCase.build(testCase.settings)
			if err == nil || !strings.Contains(err.Error(), testCase.want) || provider != nil {
				t.Fatalf("build() = %#v, %v, want error %q", provider, err, testCase.want)
			}
		})
	}
}

// The specialized providers build exactly their own pipeline and accept the
// transport they were written for.
func TestHTTPOnlyProviderConstructorsBuildOneSignalLikeRust(t *testing.T) {
	traces, err := NewHTTPTracesOtelProvider(OtelSettings{
		Environment:   "test",
		ServiceName:   "codex-code-mode-host",
		TraceExporter: OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: "https://traces.test/v1/traces", Protocol: OtelHTTPProtocolJSON},
	})
	if err != nil || traces == nil || traces.Traces() == nil || traces.Metrics() != nil || traces.Logs() != nil {
		t.Fatalf("trace provider = %#v, %v", traces, err)
	}
	_ = traces.Shutdown(context.Background())

	metrics, err := NewHTTPMetricsOtelProvider(OtelSettings{
		Environment:     "test",
		ServiceName:     "codex-wfp-setup",
		MetricsExporter: OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: "https://metrics.test/v1/metrics", Protocol: OtelHTTPProtocolJSON},
	})
	if err != nil || metrics == nil || metrics.Metrics() == nil || metrics.Traces() != nil || metrics.Logs() != nil {
		t.Fatalf("metrics provider = %#v, %v", metrics, err)
	}
	_ = metrics.Shutdown(context.Background())

	previous := StatsigMetricsRouteEnabled
	defer func() { StatsigMetricsRouteEnabled = previous }()
	StatsigMetricsRouteEnabled = "true"
	statsig, err := NewHTTPMetricsOtelProvider(OtelSettings{MetricsExporter: OtelExporter{Kind: OtelExporterStatsig}})
	if err != nil || statsig == nil || statsig.Metrics() == nil {
		t.Fatalf("statsig metrics provider = %#v, %v", statsig, err)
	}
	_ = statsig.Shutdown(context.Background())

	// A disabled specialized provider still reports nothing to build.
	if disabled, err := NewHTTPTracesOtelProvider(OtelSettings{TraceExporter: OtelExporter{Kind: OtelExporterNone}}); err != nil || disabled != nil {
		t.Fatalf("disabled trace provider = %#v, %v", disabled, err)
	}
}
