package appserver

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"codex_go/config"
)

// TestRuntimeRouterConfigReadRedactsOtelExporterHeadersLikeRust covers Rust
// #57654: config/read never returns OTEL exporter credentials, neither from the
// effective config nor from an included source layer.
func TestRuntimeRouterConfigReadRedactsOtelExporterHeadersLikeRust(t *testing.T) {
	home := t.TempDir()
	body := `
[otel.exporter."otlp-http"]
endpoint = "https://collector.example/v1/logs"
protocol = "binary"

[otel.exporter."otlp-http".headers]
authorization = "Bearer secret"
x-tenant = "acme"
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	response := router.Handle(requestWithParams(t, IntID(1), MethodConfigRead, config.ConfigReadParams{IncludeLayers: true}))
	if response.Error != nil {
		t.Fatalf("config/read error: %+v", response.Error)
	}
	read := response.Result.(*config.ConfigReadResponse)
	encoded, err := json.Marshal(read)
	if err != nil {
		t.Fatalf("Marshal config/read error = %v", err)
	}
	for _, secret := range []string{"Bearer secret", "acme"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("config/read leaked OTEL header value %q: %s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), "[redacted]") {
		t.Fatalf("config/read omitted redaction: %s", encoded)
	}
	if len(read.Layers) != 1 {
		t.Fatalf("layers = %+v, want the user layer", read.Layers)
	}
	layer, ok := read.Layers[0].Config.(map[string]any)
	if !ok {
		t.Fatalf("layer config = %T", read.Layers[0].Config)
	}
	headers := otelHeadersForTest(t, layer)
	if _, ok := headers["authorization"]; !ok {
		t.Fatalf("layer headers = %#v, want header names preserved", headers)
	}
}

// TestRedactConfigWriteOtelMasksOverriddenEffectiveValue covers the
// config/value/write projection: overridden metadata can echo an exporter or a
// single header value, so every header value is masked.
func TestRedactConfigWriteOtelMasksOverriddenEffectiveValue(t *testing.T) {
	response := &config.ConfigWriteResponse{OverriddenMetadata: &config.OverriddenMetadata{
		EffectiveValue: map[string]any{
			"otel": map[string]any{
				"exporter": map[string]any{"otlp-http": map[string]any{
					"endpoint": "https://collector.example/v1/logs",
					"headers":  map[string]any{"authorization": "Bearer secret"},
				}},
			},
		},
	}}
	redactConfigWriteOtel(response)
	headers := otelHeadersForTest(t, response.OverriddenMetadata.EffectiveValue.(map[string]any))
	if headers["authorization"] != "[redacted]" {
		t.Fatalf("overridden headers = %#v, want redacted values", headers)
	}
	noMetadata := &config.ConfigWriteResponse{}
	redactConfigWriteOtel(noMetadata)
	redactConfigWriteOtel(nil)
}

func otelHeadersForTest(t *testing.T, values map[string]any) map[string]any {
	t.Helper()
	otel, ok := values["otel"].(map[string]any)
	if !ok {
		t.Fatalf("otel = %#v, want a table", values["otel"])
	}
	exporter, ok := otel["exporter"].(map[string]any)
	if !ok {
		t.Fatalf("otel.exporter = %#v, want a table", otel["exporter"])
	}
	body, ok := exporter["otlp-http"].(map[string]any)
	if !ok {
		t.Fatalf("otel.exporter.otlp-http = %#v, want a table", exporter)
	}
	headers, ok := body["headers"].(map[string]any)
	if !ok {
		t.Fatalf("otel.exporter.otlp-http.headers = %#v, want a table", body)
	}
	return headers
}
