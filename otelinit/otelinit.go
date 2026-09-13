// Package otelinit mirrors codex-rs/core/src/otel_init.rs: it maps the resolved
// Codex config onto codex-otel settings and builds the OTEL provider.
//
// Go builds the metrics, logging, and tracing pipelines. The provider build
// never fails for an unsupported transport; the telemetry package reports it
// and stays disabled.
package otelinit

import (
	"codex_go/config"
	"codex_go/telemetry"
)

// Options configures BuildProvider.
type Options struct {
	// Config is the effective config whose `otel` table is resolved. A nil
	// config uses the OTEL defaults.
	Config *config.Config
	// ServiceName is the OTEL resource service name (Rust's
	// `service_name_override`), for example "codex-app-server".
	ServiceName string
	// ServiceVersion is the OTEL resource service version.
	ServiceVersion string
	// DefaultAnalyticsEnabled is the analytics default used when the config does
	// not set `analytics.enabled`; metrics export requires analytics.
	DefaultAnalyticsEnabled bool
}

// BuildProvider mirrors otel_init::build_provider: resolve the effective
// `otel` settings, disable the metrics exporter unless analytics is enabled
// (the log exporter is not gated), and build the provider from the mapped
// settings.
func BuildProvider(options Options) (*telemetry.OtelProvider, error) {
	resolved := config.DefaultOtelConfig()
	analyticsEnabled := options.DefaultAnalyticsEnabled
	if options.Config != nil {
		resolved = options.Config.Otel()
		analyticsEnabled = options.Config.AnalyticsEnabled(options.DefaultAnalyticsEnabled)
	}
	metricsExporter := resolved.MetricsExporter
	if !analyticsEnabled {
		metricsExporter = config.OtelExporterKind{Kind: config.OtelExporterKindNone}
	}
	runtimeMetrics := false
	if options.Config != nil {
		runtimeMetrics = options.Config.FeatureSettings()["runtime_metrics"]
	}
	return telemetry.NewOtelProvider(telemetry.OtelSettings{
		Environment:     resolved.Environment,
		ServiceName:     options.ServiceName,
		ServiceVersion:  options.ServiceVersion,
		Exporter:        otelExporter(resolved.Exporter),
		TraceExporter:   otelExporter(resolved.TraceExporter),
		MetricsExporter: otelExporter(metricsExporter),
		RuntimeMetrics:  runtimeMetrics,
		SpanAttributes:  resolved.SpanAttributes,
		Tracestate:      resolved.Tracestate,
	})
}

// otelExporter maps a resolved config exporter kind onto codex-otel's
// OtelExporter (otel_init's `to_otel_exporter`).
func otelExporter(kind config.OtelExporterKind) telemetry.OtelExporter {
	switch kind.Kind {
	case config.OtelExporterKindStatsig:
		return telemetry.OtelExporter{Kind: telemetry.OtelExporterStatsig}
	case config.OtelExporterKindOtlpHTTP:
		return telemetry.OtelExporter{
			Kind:     telemetry.OtelExporterOtlpHTTP,
			Endpoint: kind.Endpoint,
			Headers:  cloneHeaders(kind.Headers),
			Protocol: kind.Protocol,
			TLS:      otelTLSConfig(kind.TLS),
		}
	case config.OtelExporterKindOtlpGRPC:
		return telemetry.OtelExporter{
			Kind:     telemetry.OtelExporterOtlpGRPC,
			Endpoint: kind.Endpoint,
			Headers:  cloneHeaders(kind.Headers),
			TLS:      otelTLSConfig(kind.TLS),
		}
	default:
		return telemetry.OtelExporter{Kind: telemetry.OtelExporterNone}
	}
}

func otelTLSConfig(tlsConfig *config.OtelTLSConfig) *telemetry.OTLPHTTPTLSConfig {
	if tlsConfig == nil {
		return nil
	}
	return &telemetry.OTLPHTTPTLSConfig{
		CACertificate:     tlsConfig.CACertificate,
		ClientCertificate: tlsConfig.ClientCertificate,
		ClientPrivateKey:  tlsConfig.ClientPrivateKey,
	}
}

func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}
