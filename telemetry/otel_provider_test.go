package telemetry

import (
	"context"
	"testing"
)

// resolve_exporter: the built-in Statsig route is only resolved by a build that
// opts into it (Rust's cfg!(debug_assertions) guard), and otherwise becomes
// "none".
func TestResolveOtelExporterStatsigLikeRust(t *testing.T) {
	previous := StatsigMetricsRouteEnabled
	defer func() { StatsigMetricsRouteEnabled = previous }()

	StatsigMetricsRouteEnabled = "false"
	if resolved := ResolveOtelExporter(OtelExporter{Kind: OtelExporterStatsig}); resolved.Kind != OtelExporterNone {
		t.Fatalf("debug-build resolution = %#v", resolved)
	}

	StatsigMetricsRouteEnabled = "true"
	resolved := ResolveOtelExporter(OtelExporter{Kind: OtelExporterStatsig})
	if resolved.Kind != OtelExporterOtlpHTTP || resolved.Endpoint != StatsigMetricsEndpoint ||
		resolved.Protocol != OtelHTTPProtocolJSON || resolved.Headers[StatsigMetricsAPIKeyHeader] != StatsigMetricsAPIKey {
		t.Fatalf("Statsig resolution = %#v", resolved)
	}
	// Every other exporter is returned unchanged.
	custom := OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: "https://x"}
	if got := ResolveOtelExporter(custom); got.Endpoint != "https://x" {
		t.Fatalf("custom exporter = %#v", got)
	}
}

// try_new returns no provider when no supported exporter is enabled, and a
// metrics-only provider for the OTLP HTTP JSON exporter the Go client speaks.
func TestNewOtelProviderMetricsOnlyLikeRust(t *testing.T) {
	if provider, err := NewOtelProvider(OtelSettings{
		MetricsExporter: OtelExporter{Kind: OtelExporterNone},
	}); err != nil || provider != nil {
		t.Fatalf("disabled provider = %#v err = %v", provider, err)
	}
	// An exporter with no endpoint cannot build an export.
	if provider, err := NewOtelProvider(OtelSettings{
		MetricsExporter: OtelExporter{Kind: OtelExporterOtlpHTTP, Protocol: OtelHTTPProtocolJSON},
	}); err != nil || provider != nil {
		t.Fatalf("empty-endpoint provider = %#v err = %v", provider, err)
	}
	// The gRPC transport Rust supports has no Go exporter, so the provider stays
	// disabled instead of failing.
	if provider, err := NewOtelProvider(OtelSettings{
		MetricsExporter: OtelExporter{Kind: OtelExporterOtlpGRPC, Endpoint: "https://metrics.test:4317"},
	}); err != nil || provider != nil {
		t.Fatalf("unsupported provider = %#v err = %v", provider, err)
	}
	// The OTLP/HTTP binary protocol is supported.
	binary, err := NewOtelProvider(OtelSettings{
		MetricsExporter: OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: "https://metrics.test/v1/metrics", Protocol: OtelHTTPProtocolBinary},
	})
	if err != nil || binary == nil || binary.Metrics() == nil || binary.Metrics().exporter.protocol != OtelHTTPProtocolBinary {
		t.Fatalf("binary provider = %#v err = %v", binary, err)
	}
	_ = binary.Shutdown(context.Background())

	provider, err := NewOtelProvider(OtelSettings{
		Environment:     "test",
		ServiceName:     "codex-app-server",
		ServiceVersion:  "1.0.2",
		MetricsExporter: OtelExporter{Kind: OtelExporterOtlpHTTP, Endpoint: "https://metrics.test/v1/metrics", Protocol: OtelHTTPProtocolJSON},
	})
	if err != nil || provider == nil || provider.Metrics() == nil {
		t.Fatalf("provider = %#v err = %v", provider, err)
	}
	if !provider.Metrics().Enabled() {
		t.Fatal("metrics client is disabled")
	}
	if got := provider.Metrics().exporter.endpoint; got != "https://metrics.test/v1/metrics" {
		t.Fatalf("endpoint = %q", got)
	}

	// The metrics resource carries the service name/version and the environment,
	// and the process-start metric is recorded once with the bounded originator.
	doer := &recordingHTTPDoer{}
	provider.Metrics().exporter.httpClient = doer
	resetProcessStartLatch()
	if !RecordProcessStartOnce(provider.Metrics(), "codex-app-server") {
		t.Fatal("the first process start was not recorded")
	}
	if RecordProcessStartOnce(provider.Metrics(), "codex-app-server") {
		t.Fatal("the process start was recorded twice")
	}
	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	resetProcessStartLatch()

	requests := doer.snapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %#v", requests)
	}
	resourceMetrics := requests[0].body["resourceMetrics"].([]any)
	attributes := attributeMap(resourceMetrics[0].(map[string]any)["resource"].(map[string]any)["attributes"])
	if attributes["service.name"] != "codex-app-server" || attributes["service.version"] != "1.0.2" || attributes["env"] != "test" {
		t.Fatalf("resource attributes = %#v", attributes)
	}
	metrics := metricsByName(t, requests[0])
	point := metrics[ProcessStartMetric]["sum"].(map[string]any)["dataPoints"].([]any)[0].(map[string]any)
	if originator := attributeMap(point["attributes"])[OriginatorTag]; originator != "codex-app-server" {
		t.Fatalf("originator tag = %q", originator)
	}
}

// The process-start counter is tagged with the bounded originator value.
func TestRecordProcessStartOnceBoundedOriginator(t *testing.T) {
	resetProcessStartLatch()
	defer resetProcessStartLatch()
	doer := &recordingHTTPDoer{}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{})
	if !RecordProcessStartOnce(client, "unknown originator!") {
		t.Fatal("the process start was not recorded")
	}
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	metrics := metricsByName(t, doer.snapshot()[0])
	point := metrics[ProcessStartMetric]["sum"].(map[string]any)["dataPoints"].([]any)[0].(map[string]any)
	if originator := attributeMap(point["attributes"])[OriginatorTag]; originator != "other" {
		t.Fatalf("originator tag = %q", originator)
	}
}
