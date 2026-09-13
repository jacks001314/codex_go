package otelinit

import (
	"testing"

	"codex_go/config"
	"codex_go/telemetry"
)

func otlpHTTPConfig() *config.Config {
	return &config.Config{Values: map[string]any{
		"analytics": map[string]any{"enabled": true},
		"otel": map[string]any{
			"environment": "test",
			"metrics_exporter": map[string]any{
				"otlp-http": map[string]any{
					"endpoint": "https://metrics.test/v1/metrics",
					"protocol": config.OtelHTTPProtocolJSON,
				},
			},
		},
	}}
}

// The default metrics exporter is Statsig, which the built-in route guard keeps
// inert in development builds, so no provider is built.
func TestBuildProviderDefaultsToNoExport(t *testing.T) {
	previous := telemetry.StatsigMetricsRouteEnabled
	defer func() { telemetry.StatsigMetricsRouteEnabled = previous }()
	telemetry.StatsigMetricsRouteEnabled = "false"

	if provider, err := BuildProvider(Options{}); err != nil || provider != nil {
		t.Fatalf("nil-config provider = %#v err = %v", provider, err)
	}
	if provider, err := BuildProvider(Options{Config: &config.Config{}}); err != nil || provider != nil {
		t.Fatalf("default provider = %#v err = %v", provider, err)
	}
}

// An opted-in build resolves the Statsig route to its OTLP/HTTP exporter.
func TestBuildProviderStatsigRouteOptIn(t *testing.T) {
	previous := telemetry.StatsigMetricsRouteEnabled
	defer func() { telemetry.StatsigMetricsRouteEnabled = previous }()
	telemetry.StatsigMetricsRouteEnabled = "true"

	provider, err := BuildProvider(Options{
		Config: &config.Config{Values: map[string]any{
			"analytics": map[string]any{"enabled": true},
		}},
		ServiceName:    "codex-app-server",
		ServiceVersion: "1.0.2",
	})
	if err != nil || provider == nil || provider.Metrics() == nil {
		t.Fatalf("provider = %#v err = %v", provider, err)
	}
	if !provider.Metrics().Enabled() {
		t.Fatal("the opted-in Statsig route did not build a metrics client")
	}
	if err := provider.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

// The OTLP HTTP exporter requires analytics: with analytics disabled the
// exporter resolves to none and no provider is built.
func TestBuildProviderRequiresAnalytics(t *testing.T) {
	provider, err := BuildProvider(Options{Config: otlpHTTPConfig(), ServiceName: "codex-app-server"})
	if err != nil || provider == nil || provider.Metrics() == nil || !provider.Metrics().Enabled() {
		t.Fatalf("analytics-enabled provider = %#v err = %v", provider, err)
	}
	if err := provider.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	disabled := otlpHTTPConfig()
	disabled.Values["analytics"] = map[string]any{"enabled": false}
	if provider, err := BuildProvider(Options{Config: disabled, DefaultAnalyticsEnabled: true}); err != nil || provider != nil {
		t.Fatalf("analytics-disabled provider = %#v err = %v", provider, err)
	}

	// Without an `analytics` table the caller's default decides.
	noAnalytics := otlpHTTPConfig()
	delete(noAnalytics.Values, "analytics")
	if provider, err := BuildProvider(Options{Config: noAnalytics, DefaultAnalyticsEnabled: false}); err != nil || provider != nil {
		t.Fatalf("default-disabled provider = %#v err = %v", provider, err)
	}
	provider, err = BuildProvider(Options{Config: noAnalytics, DefaultAnalyticsEnabled: true, ServiceName: "codex-app-server"})
	if err != nil || provider == nil || provider.Metrics() == nil || !provider.Metrics().Enabled() {
		t.Fatalf("default-enabled provider = %#v err = %v", provider, err)
	}
	if err := provider.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

// The gRPC transport Go cannot speak leaves the provider disabled instead of
// failing startup.
func TestBuildProviderUnsupportedTransportsStayDisabled(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{
		"analytics": map[string]any{"enabled": true},
		"otel": map[string]any{"metrics_exporter": map[string]any{
			"otlp-grpc": map[string]any{"endpoint": "https://metrics.test:4317"},
		}},
	}}
	if provider, err := BuildProvider(Options{Config: cfg, ServiceName: "codex-app-server"}); err != nil || provider != nil {
		t.Fatalf("gRPC provider = %#v err = %v", provider, err)
	}
}

// The OTLP/HTTP binary protocol builds a working provider.
func TestBuildProviderOTLPHTTPBinary(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{
		"analytics": map[string]any{"enabled": true},
		"otel": map[string]any{"metrics_exporter": map[string]any{
			"otlp-http": map[string]any{
				"endpoint": "https://metrics.test/v1/metrics",
				"protocol": config.OtelHTTPProtocolBinary,
			},
		}},
	}}
	provider, err := BuildProvider(Options{Config: cfg, ServiceName: "codex-app-server"})
	if err != nil || provider == nil || provider.Metrics() == nil || !provider.Metrics().Enabled() {
		t.Fatalf("binary provider = %#v err = %v", provider, err)
	}
	if err := provider.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
