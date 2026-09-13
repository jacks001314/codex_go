package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Mirrors Rust's typed OtelConfigToml deserialization: the accepted shapes pass
// and every malformed shape fails the load.
func TestValidateOtelConfigValuesLikeRust(t *testing.T) {
	for _, values := range []map[string]any{
		{},
		{"otel": map[string]any{}},
		{"otel": map[string]any{"log_user_prompt": true, "environment": "test"}},
		{"otel": map[string]any{"tool_result": map[string]any{"max_bytes": int64(4096)}}},
		{"otel": map[string]any{"span_attributes": map[string]any{"a.b": "c"}}},
		{"otel": map[string]any{"tracestate": map[string]any{"member": map[string]any{"field": "value"}}}},
		{"otel": map[string]any{"exporter": "none", "trace_exporter": "statsig", "metrics_exporter": "none"}},
		{"otel": map[string]any{"metrics_exporter": map[string]any{"otlp-http": map[string]any{
			"endpoint": "https://metrics.test/v1/metrics",
			"protocol": OtelHTTPProtocolJSON,
			"headers":  map[string]any{"authorization": "Bearer t"},
			"tls":      map[string]any{"ca_certificate": "/etc/ca.pem"},
		}}}},
		{"otel": map[string]any{"trace_exporter": map[string]any{"otlp-grpc": map[string]any{
			"endpoint": "https://traces.test:4317",
		}}}},
		// Unknown fields inside the otel table are ignored, as serde does.
		{"otel": map[string]any{"future_option": "x"}},
	} {
		if err := ValidateOtelConfigValues(values); err != nil {
			t.Fatalf("ValidateOtelConfigValues(%#v) error = %v", values, err)
		}
	}

	rejected := []struct {
		name   string
		values map[string]any
	}{
		{"non-table otel", map[string]any{"otel": "x"}},
		{"non-bool log_user_prompt", map[string]any{"otel": map[string]any{"log_user_prompt": "yes"}}},
		{"non-string environment", map[string]any{"otel": map[string]any{"environment": 3}}},
		{"non-table tool_result", map[string]any{"otel": map[string]any{"tool_result": "x"}}},
		{"non-integer max_bytes", map[string]any{"otel": map[string]any{"tool_result": map[string]any{"max_bytes": "many"}}}},
		{"negative max_bytes", map[string]any{"otel": map[string]any{"tool_result": map[string]any{"max_bytes": int64(-1)}}}},
		{"non-table span_attributes", map[string]any{"otel": map[string]any{"span_attributes": "x"}}},
		{"non-string span attribute", map[string]any{"otel": map[string]any{"span_attributes": map[string]any{"a": 1}}}},
		{"non-table tracestate", map[string]any{"otel": map[string]any{"tracestate": "x"}}},
		{"non-table tracestate member", map[string]any{"otel": map[string]any{"tracestate": map[string]any{"member": "x"}}}},
		{"non-string tracestate field", map[string]any{"otel": map[string]any{"tracestate": map[string]any{"member": map[string]any{"field": 1}}}}},
		{"unknown exporter variant", map[string]any{"otel": map[string]any{"exporter": "otlp"}}},
		{"unknown metrics exporter variant", map[string]any{"otel": map[string]any{"metrics_exporter": map[string]any{"otlp-tcp": map[string]any{"endpoint": "x"}}}}},
		{"two exporter variants", map[string]any{"otel": map[string]any{"exporter": map[string]any{
			"otlp-http": map[string]any{"endpoint": "https://x", "protocol": OtelHTTPProtocolJSON},
			"otlp-grpc": map[string]any{"endpoint": "https://y"},
		}}}},
		{"missing http endpoint", map[string]any{"otel": map[string]any{"exporter": map[string]any{"otlp-http": map[string]any{"protocol": OtelHTTPProtocolJSON}}}}},
		{"missing http protocol", map[string]any{"otel": map[string]any{"exporter": map[string]any{"otlp-http": map[string]any{"endpoint": "https://x"}}}}},
		{"unknown http protocol", map[string]any{"otel": map[string]any{"exporter": map[string]any{"otlp-http": map[string]any{"endpoint": "https://x", "protocol": "grpc"}}}}},
		{"missing grpc endpoint", map[string]any{"otel": map[string]any{"exporter": map[string]any{"otlp-grpc": map[string]any{}}}}},
		{"non-string header", map[string]any{"otel": map[string]any{"exporter": map[string]any{"otlp-http": map[string]any{
			"endpoint": "https://x", "protocol": OtelHTTPProtocolJSON, "headers": map[string]any{"a": 1},
		}}}}},
		{"non-table tls", map[string]any{"otel": map[string]any{"exporter": map[string]any{"otlp-http": map[string]any{
			"endpoint": "https://x", "protocol": OtelHTTPProtocolJSON, "tls": "x",
		}}}}},
		{"non-string tls path", map[string]any{"otel": map[string]any{"exporter": map[string]any{"otlp-http": map[string]any{
			"endpoint": "https://x", "protocol": OtelHTTPProtocolJSON, "tls": map[string]any{"ca_certificate": 1},
		}}}}},
	}
	for _, testCase := range rejected {
		if err := ValidateOtelConfigValues(testCase.values); err == nil {
			t.Fatalf("%s: ValidateOtelConfigValues(%#v) = nil, want an error", testCase.name, testCase.values)
		}
	}
}

// A malformed otel table fails the config load instead of being ignored
// (Rust's typed deserialization), while a valid one loads.
func TestLoadRejectsMalformedOtelConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.toml")
	if err := os.WriteFile(configPath, []byte("otel = \"enabled\"\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	if _, err := LoadEffective(home, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "otel must be a table") {
		t.Fatalf("malformed otel error = %v", err)
	}

	if err := os.WriteFile(configPath, []byte("[otel]\nenvironment = \"staging\"\n\n[otel.metrics_exporter.otlp-http]\nendpoint = \"https://metrics.test/v1/metrics\"\nprotocol = \"json\"\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	cfg, err := LoadEffective(home, nil, nil, nil)
	if err != nil {
		t.Fatalf("valid otel config error = %v", err)
	}
	if resolved := cfg.Otel(); resolved.Environment != "staging" || resolved.MetricsExporter.Kind != OtelExporterKindOtlpHTTP {
		t.Fatalf("resolved otel = %#v", resolved)
	}
}
