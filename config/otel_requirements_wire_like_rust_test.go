package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestConfigRequirementsOtelWireShapeLikeRust covers Rust #57638/#57654: the
// configRequirements/read projection exposes exactly the protocol OTEL fields
// (no TOML-only log_sse_deltas/span_attributes/tracestate) and never returns
// exporter header values.
func TestConfigRequirementsOtelWireShapeLikeRust(t *testing.T) {
	home := t.TempDir()
	body := `
[otel]
log_sse_deltas = true
log_user_prompt = false
log_agent_responses = true
log_guardian_assessments = true
environment = "prod"

[otel.tool_result]
max_bytes = 4096

[otel.span_attributes]
team = "core"

[otel.tracestate."acme.com"]
tenant = "t1"

[otel.exporter."otlp-http"]
endpoint = "https://collector.example/v1/logs"
protocol = "json"

[otel.exporter."otlp-http".headers]
authorization = "Bearer secret"
`
	path := filepath.Join(home, "requirements.toml")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	service := NewConfigService(home)
	requirements := service.Requirements()
	encoded, err := json.Marshal(requirements)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}
	otel, ok := decoded["requirements"].(map[string]any)["otel"].(map[string]any)
	if !ok {
		t.Fatalf("otel = %#v", decoded["requirements"])
	}
	want := map[string]bool{
		"tool_result":              true,
		"log_user_prompt":          true,
		"log_agent_responses":      true,
		"log_guardian_assessments": true,
		"environment":              true,
		"exporter":                 true,
		"trace_exporter":           true,
		"metrics_exporter":         true,
	}
	for key := range otel {
		if !want[key] {
			t.Fatalf("otel wire leaked TOML-only field %q: %#v", key, otel)
		}
	}
	for key := range want {
		if _, ok := otel[key]; !ok {
			t.Fatalf("otel wire missing %q: %#v", key, otel)
		}
	}
	if otel["environment"] != "prod" {
		t.Fatalf("environment = %#v", otel["environment"])
	}
	toolResult, ok := otel["tool_result"].(map[string]any)
	if !ok || toolResult["max_bytes"] != float64(4096) {
		t.Fatalf("tool_result = %#v", otel["tool_result"])
	}
	exporter, ok := otel["exporter"].(map[string]any)["otlp-http"].(map[string]any)
	if !ok {
		t.Fatalf("exporter = %#v", otel["exporter"])
	}
	if exporter["protocol"] != "json" || exporter["endpoint"] != "https://collector.example/v1/logs" {
		t.Fatalf("exporter body = %#v", exporter)
	}
	headers, ok := exporter["headers"].(map[string]any)
	if !ok || headers["authorization"] != "[redacted]" {
		t.Fatalf("headers = %#v", exporter["headers"])
	}
}
