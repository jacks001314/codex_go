package config

import (
	"strings"
	"testing"
)

// Mirrors Rust's load_config_applies_otel_trace_metadata.
func TestResolveOtelTraceMetadataKeepsValidEntries(t *testing.T) {
	values := map[string]any{
		"otel": map[string]any{
			"span_attributes": map[string]any{"example.trace_attr": "enabled"},
			"tracestate": map[string]any{
				"example": map[string]any{"alpha": "one", "beta": "two"},
			},
		},
	}
	metadata, warnings := ResolveOtelTraceMetadata(values)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
	if len(metadata.SpanAttributes) != 1 || metadata.SpanAttributes["example.trace_attr"] != "enabled" {
		t.Fatalf("span attributes = %#v", metadata.SpanAttributes)
	}
	fields := metadata.Tracestate["example"]
	if len(fields) != 2 || fields["alpha"] != "one" || fields["beta"] != "two" {
		t.Fatalf("tracestate = %#v", metadata.Tracestate)
	}
}

// Config.Otel exposes the resolved settings like Rust's Config::otel.
func TestConfigOtelResolvesSettings(t *testing.T) {
	cfg := &Config{Values: map[string]any{
		"otel": map[string]any{
			"environment":      "prod",
			"metrics_exporter": "none",
			"tool_result":      map[string]any{"max_bytes": int64(1024)},
		},
	}}
	resolved := cfg.Otel()
	if resolved.Environment != "prod" || resolved.MetricsExporter.Kind != OtelExporterKindNone {
		t.Fatalf("resolved = %#v", resolved)
	}
	if resolved.ToolResult.MaxBytes != 1024 {
		t.Fatalf("tool result bytes = %d", resolved.ToolResult.MaxBytes)
	}
	if (&Config{}).Otel().MetricsExporter.Kind != OtelExporterKindStatsig {
		t.Fatal("an empty config did not keep the Statsig metrics default")
	}
}

// Mirrors Rust's load_config_drops_invalid_otel_trace_metadata_entries.
func TestResolveOtelTraceMetadataDropsInvalidEntries(t *testing.T) {
	values := map[string]any{
		"otel": map[string]any{
			"environment":     "test",
			"span_attributes": map[string]any{"": "missing-key", "example.trace_attr": "enabled"},
			"tracestate": map[string]any{
				"example": map[string]any{"alpha": "one", "beta": "two\ntoo"},
				"bad":     map[string]any{"alpha": "one\ntwo"},
			},
		},
	}
	metadata, warnings := ResolveOtelTraceMetadata(values)
	if len(metadata.SpanAttributes) != 1 || metadata.SpanAttributes["example.trace_attr"] != "enabled" {
		t.Fatalf("span attributes = %#v", metadata.SpanAttributes)
	}
	fields := metadata.Tracestate["example"]
	if len(fields) != 1 || fields["alpha"] != "one" {
		t.Fatalf("tracestate = %#v", metadata.Tracestate)
	}
	if _, ok := metadata.Tracestate["bad"]; ok {
		t.Fatalf("an invalid member survived: %#v", metadata.Tracestate)
	}
	for _, want := range []string{
		"Ignoring invalid `otel.span_attributes` config: configured span attribute key must not be empty",
		"Ignoring invalid `otel.tracestate` config: invalid configured tracestate value for example.beta",
		"Ignoring invalid `otel.tracestate` config: invalid configured tracestate value for bad.alpha",
	} {
		if !containsWarning(warnings, want) {
			t.Fatalf("warnings = %#v, missing %q", warnings, want)
		}
	}
}

// The member key is validated by the W3C tracestate grammar through
// opentelemetry's TraceState, and the joined field value must stay header-safe.
func TestResolveOtelTraceMetadataValidatesMemberKeysAndHeaderValues(t *testing.T) {
	values := map[string]any{
		"otel": map[string]any{
			"tracestate": map[string]any{
				"UpperCase": map[string]any{"alpha": "one"},
				"trailing":  map[string]any{"alpha": "one "},
				"long":      map[string]any{"alpha": strings.Repeat("a", 257)},
			},
		},
	}
	metadata, warnings := ResolveOtelTraceMetadata(values)
	if len(metadata.Tracestate) != 0 {
		t.Fatalf("tracestate = %#v, want none", metadata.Tracestate)
	}
	for _, want := range []string{
		"UpperCase is not a valid key in TraceState, see https://www.w3.org/TR/trace-context/#key for more details",
		"invalid configured tracestate value for trailing",
		"is not a valid value in TraceState, see https://www.w3.org/TR/trace-context/#value for more details",
	} {
		if !containsWarning(warnings, want) {
			t.Fatalf("warnings = %#v, missing %q", warnings, want)
		}
	}
	for _, warning := range warnings {
		if !strings.HasPrefix(warning, "Ignoring invalid `otel.tracestate` config: ") {
			t.Fatalf("unexpected warning %q", warning)
		}
	}
}

// The otel warnings are part of config.startup_warnings even without managed
// requirements, and they follow the requirement warnings.
func TestStartupWarningsIncludeOtelWarnings(t *testing.T) {
	values := map[string]any{
		"otel": map[string]any{
			"span_attributes": map[string]any{"": "missing-key"},
		},
	}
	warnings := StartupWarnings(values, nil)
	if !containsWarning(warnings, "Ignoring invalid `otel.span_attributes` config: configured span attribute key must not be empty") {
		t.Fatalf("StartupWarnings() = %#v", warnings)
	}

	requirement := "model_provider"
	managed := "test"
	withRequirement := StartupWarnings(map[string]any{
		"model_provider": "configured",
		"otel":           values["otel"],
	}, &ConfigRequirements{ModelProvider: &managed})
	if len(withRequirement) != 2 {
		t.Fatalf("StartupWarnings() = %#v, want the requirement warning then the otel warning", withRequirement)
	}
	if !strings.Contains(withRequirement[0], requirement) {
		t.Fatalf("first warning = %q, want the requirement warning", withRequirement[0])
	}
}

// Without an `otel` table nothing is reported.
func TestOtelStartupWarningsAbsentTable(t *testing.T) {
	if warnings := OtelStartupWarnings(map[string]any{}); len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if metadata, warnings := ResolveOtelTraceMetadata(map[string]any{}); len(warnings) != 0 || len(metadata.SpanAttributes) != 0 || len(metadata.Tracestate) != 0 {
		t.Fatalf("metadata = %#v warnings = %#v", metadata, warnings)
	}
}

func containsWarning(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}

// Mirrors OtelConfig::default and Rust's metrics_exporter_defaults_to_statsig.
func TestResolveOtelConfigDefaults(t *testing.T) {
	resolved, warnings := ResolveOtelConfig(map[string]any{})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if resolved.MetricsExporter.Kind != OtelExporterKindStatsig {
		t.Fatalf("metrics exporter = %#v", resolved.MetricsExporter)
	}
	if resolved.Exporter.Kind != OtelExporterKindNone || resolved.TraceExporter.Kind != OtelExporterKindNone {
		t.Fatalf("log/trace exporter = %#v / %#v", resolved.Exporter, resolved.TraceExporter)
	}
	if resolved.Environment != DefaultOtelEnvironment || resolved.LogUserPrompt {
		t.Fatalf("environment = %q log_user_prompt = %v", resolved.Environment, resolved.LogUserPrompt)
	}
	if resolved.ToolResult.MaxBytes != 2*1024 {
		t.Fatalf("tool result bytes = %d", resolved.ToolResult.MaxBytes)
	}
}

// Mirrors Rust's trace_exporter_defaults_to_none_when_log_exporter_is_set: an
// OTLP HTTP log exporter must not implicitly enable a trace exporter, and the
// tool-result limit is read from its table.
func TestResolveOtelConfigExporterKinds(t *testing.T) {
	resolved, warnings := ResolveOtelConfig(map[string]any{
		"otel": map[string]any{
			"exporter": map[string]any{
				"otlp-http": map[string]any{
					"endpoint": "http://localhost:14318/v1/logs",
					"protocol": OtelHTTPProtocolBinary,
				},
			},
			"metrics_exporter": "none",
			"tool_result":      map[string]any{"max_bytes": int64(8192)},
		},
	})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v", warnings)
	}
	if resolved.ToolResult.MaxBytes != 8192 {
		t.Fatalf("tool result bytes = %d", resolved.ToolResult.MaxBytes)
	}
	if resolved.Exporter.Kind != OtelExporterKindOtlpHTTP ||
		resolved.Exporter.Endpoint != "http://localhost:14318/v1/logs" ||
		resolved.Exporter.Protocol != OtelHTTPProtocolBinary {
		t.Fatalf("log exporter = %#v", resolved.Exporter)
	}
	if resolved.TraceExporter.Kind != OtelExporterKindNone {
		t.Fatalf("trace exporter = %#v", resolved.TraceExporter)
	}
	if resolved.MetricsExporter.Kind != OtelExporterKindNone {
		t.Fatalf("metrics exporter = %#v", resolved.MetricsExporter)
	}
}

// OTLP HTTP and gRPC kinds carry their endpoint, headers, protocol, and TLS.
func TestResolveOtelConfigOtlpVariants(t *testing.T) {
	resolved, _ := ResolveOtelConfig(map[string]any{
		"otel": map[string]any{
			"metrics_exporter": map[string]any{
				"otlp-http": map[string]any{
					"endpoint": "https://metrics.example/v1/metrics",
					"protocol": OtelHTTPProtocolJSON,
					"headers":  map[string]any{"statsig-api-key": "token"},
					"tls": map[string]any{
						"ca_certificate":     "/etc/ca.pem",
						"client_certificate": "/etc/client.pem",
						"client_private_key": "/etc/client.key",
					},
				},
			},
			"trace_exporter": map[string]any{
				"otlp-grpc": map[string]any{
					"endpoint": "https://traces.example:4317",
					"headers":  map[string]any{"authorization": "Bearer token"},
				},
			},
		},
	})
	metrics := resolved.MetricsExporter
	if metrics.Kind != OtelExporterKindOtlpHTTP || metrics.Protocol != OtelHTTPProtocolJSON ||
		metrics.Headers["statsig-api-key"] != "token" {
		t.Fatalf("metrics exporter = %#v", metrics)
	}
	if metrics.TLS == nil || metrics.TLS.CACertificate != "/etc/ca.pem" ||
		metrics.TLS.ClientCertificate != "/etc/client.pem" || metrics.TLS.ClientPrivateKey != "/etc/client.key" {
		t.Fatalf("metrics tls = %#v", metrics.TLS)
	}
	traces := resolved.TraceExporter
	if traces.Kind != OtelExporterKindOtlpGRPC || traces.Endpoint != "https://traces.example:4317" ||
		traces.Headers["authorization"] != "Bearer token" || traces.TLS != nil {
		t.Fatalf("trace exporter = %#v", traces)
	}
}

// environment and log_user_prompt are applied when present.
func TestResolveOtelConfigScalars(t *testing.T) {
	resolved, _ := ResolveOtelConfig(map[string]any{
		"otel": map[string]any{
			"environment":     "staging",
			"log_user_prompt": true,
		},
	})
	if resolved.Environment != "staging" || !resolved.LogUserPrompt {
		t.Fatalf("environment = %q log_user_prompt = %v", resolved.Environment, resolved.LogUserPrompt)
	}
}

// A malformed exporter kind is ignored (the Go loader keeps the default;
// Rust's typed deserialization would reject the config instead).
func TestResolveOtelConfigIgnoresMalformedExporters(t *testing.T) {
	for name, raw := range map[string]any{
		"unknown string":        "otlp",
		"http without protocol": map[string]any{"otlp-http": map[string]any{"endpoint": "https://x"}},
		"http without endpoint": map[string]any{"otlp-http": map[string]any{"protocol": OtelHTTPProtocolJSON}},
		"two variants": map[string]any{
			"otlp-http": map[string]any{"endpoint": "https://x", "protocol": OtelHTTPProtocolJSON},
			"otlp-grpc": map[string]any{"endpoint": "https://y"},
		},
	} {
		resolved, _ := ResolveOtelConfig(map[string]any{"otel": map[string]any{"exporter": raw}})
		if resolved.Exporter.Kind != OtelExporterKindNone {
			t.Fatalf("%s: exporter = %#v", name, resolved.Exporter)
		}
	}
}
