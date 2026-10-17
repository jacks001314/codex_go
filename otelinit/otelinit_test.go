package otelinit

import (
	"testing"

	"codex_go/config"
	"codex_go/metrics"
	"codex_go/telemetry"
)

// TestBuildProviderSkipGlobalMetricsInstall pins that a short-lived provider can
// be built without replacing the process-global metrics client, which the TUI
// daemon telemetry relies on.
func TestBuildProviderSkipGlobalMetricsInstall(t *testing.T) {
	sentinel := telemetry.NewMetricsClient(telemetry.MetricsClientOptions{
		Environment: "test",
		ServiceName: "sentinel",
		Endpoint:    "https://metrics.test/v1/metrics",
	})
	if sentinel == nil || !sentinel.Enabled() {
		t.Fatal("sentinel metrics client is not enabled")
	}
	telemetry.InstallGlobalMetrics(sentinel)
	t.Cleanup(func() { telemetry.InstallGlobalMetrics(nil) })

	provider, err := BuildProvider(Options{Config: otlpHTTPConfig(), SkipGlobalMetricsInstall: true})
	if err != nil {
		t.Fatalf("BuildProvider() error = %v", err)
	}
	if provider == nil || provider.Metrics() == nil {
		t.Fatalf("provider = %#v", provider)
	}
	defer func() { _ = provider.Shutdown(t.Context()) }()
	if got := metrics.Global(); got != metrics.Recorder(sentinel) {
		t.Fatalf("global metrics client = %#v, want the sentinel left in place", got)
	}
}

// Rust parity (#47408): a managed policy that restricts application destinations
// disables the OTLP log, trace and metric exports.
func TestBuildProviderDisablesOtlpUnderRestrictedApplicationNetworkPolicy(t *testing.T) {
	cfg := otlpHTTPConfig()
	cfg.Requirements = &config.ConfigRequirements{Application: &config.ApplicationRequirements{
		Network: &config.ApplicationNetworkRequirements{
			Enabled: true,
			Domains: map[string]config.NetworkPermission{"allowed.example": config.NetworkAllow},
		},
	}}
	cfg.BindApplicationNetworkPolicy()
	if !cfg.RestrictsApplicationTraffic() {
		t.Fatal("the bound policy does not restrict traffic")
	}
	provider, err := BuildProvider(Options{Config: cfg, ServiceName: "codex-app-server"})
	if err != nil {
		t.Fatalf("BuildProvider() error = %v", err)
	}
	if provider == nil {
		return
	}
	defer func() { _ = provider.Shutdown(t.Context()) }()
	if metrics := provider.Metrics(); metrics != nil && metrics.Enabled() {
		t.Fatal("a restricted application network policy still exported OTLP metrics")
	}

	// The same config without the restriction keeps its OTLP pipeline.
	unrestricted := otlpHTTPConfig()
	unrestricted.BindApplicationNetworkPolicy()
	allowed, err := BuildProvider(Options{Config: unrestricted, ServiceName: "codex-app-server"})
	if err != nil {
		t.Fatalf("BuildProvider(unrestricted) error = %v", err)
	}
	if allowed == nil || allowed.Metrics() == nil {
		t.Fatalf("unrestricted provider = %#v", allowed)
	}
	defer func() { _ = allowed.Shutdown(t.Context()) }()
}

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

// Rust #53859: analytics consent controls the built-in Statsig exporter only.
// An explicit OTLP collector keeps exporting even when analytics is disabled,
// and without an `analytics` table the caller's default cannot gate OTLP.
func TestBuildProviderConsentOnlyGatesBuiltInExporter(t *testing.T) {
	disabled := otlpHTTPConfig()
	disabled.Values["analytics"] = map[string]any{"enabled": false}
	provider, err := BuildProvider(Options{Config: disabled, ServiceName: "codex-app-server"})
	if err != nil || provider == nil || provider.Metrics() == nil || !provider.Metrics().Enabled() {
		t.Fatalf("analytics-disabled OTLP provider = %#v err = %v", provider, err)
	}
	if err := provider.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	// Without an `analytics` table the caller's default must not gate OTLP.
	noAnalytics := otlpHTTPConfig()
	delete(noAnalytics.Values, "analytics")
	provider, err = BuildProvider(Options{Config: noAnalytics, DefaultAnalyticsEnabled: false, ServiceName: "codex-app-server"})
	if err != nil || provider == nil || provider.Metrics() == nil || !provider.Metrics().Enabled() {
		t.Fatalf("default-disabled OTLP provider = %#v err = %v", provider, err)
	}
	if err := provider.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	// The built-in Statsig exporter is the one analytics consent gates.
	previous := telemetry.StatsigMetricsRouteEnabled
	defer func() { telemetry.StatsigMetricsRouteEnabled = previous }()
	telemetry.StatsigMetricsRouteEnabled = "true"

	statsigDisabled := &config.Config{Values: map[string]any{
		"analytics": map[string]any{"enabled": false},
	}}
	if provider, err := BuildProvider(Options{Config: statsigDisabled, DefaultAnalyticsEnabled: true}); err != nil || provider != nil {
		t.Fatalf("consentless Statsig provider = %#v err = %v", provider, err)
	}
	statsigEnabled := &config.Config{Values: map[string]any{
		"analytics": map[string]any{"enabled": true},
	}}
	provider, err = BuildProvider(Options{Config: statsigEnabled, DefaultAnalyticsEnabled: false, ServiceName: "codex-app-server"})
	if err != nil || provider == nil || provider.Metrics() == nil || !provider.Metrics().Enabled() {
		t.Fatalf("consented Statsig provider = %#v err = %v", provider, err)
	}
	if err := provider.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

// The OTLP gRPC transport builds a working provider.
func TestBuildProviderOTLPGRPC(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{
		"analytics": map[string]any{"enabled": true},
		"otel": map[string]any{"metrics_exporter": map[string]any{
			"otlp-grpc": map[string]any{"endpoint": "https://metrics.test:4317"},
		}},
	}}
	provider, err := BuildProvider(Options{Config: cfg, ServiceName: "codex-app-server"})
	if err != nil || provider == nil || provider.Metrics() == nil || !provider.Metrics().Enabled() {
		t.Fatalf("gRPC provider = %#v err = %v", provider, err)
	}
	if err := provider.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
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
