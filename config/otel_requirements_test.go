package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestParseRequirementsTOMLOtelLikeRust covers Rust's OtelRequirementsToml
// surface (Rust #57638/#57649): exact logging switches, the tool-result byte
// limit, externally tagged exporters and the trace metadata tables.
func TestParseRequirementsTOMLOtelLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[otel]
log_user_prompt = false
log_agent_responses = true
log_guardian_assessments = true
environment = "managed"
metrics_exporter = "none"

[otel.tool_result]
max_bytes = 4096

[otel.exporter."otlp-http"]
endpoint = "https://collector.example/v1/logs"
protocol = "json"

[otel.exporter."otlp-http".headers]
authorization = "Bearer managed"

[otel.trace_exporter."otlp-grpc"]
endpoint = "https://collector.example:4317"

[otel.span_attributes]
"deployment.environment" = "managed"

[otel.tracestate."acme"]
tenant = "acme"
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil || requirements.Otel == nil {
		t.Fatalf("requirements.Otel = %#v", requirements)
	}
	otel := requirements.Otel
	if otel.LogUserPrompt == nil || *otel.LogUserPrompt {
		t.Fatalf("log_user_prompt = %#v, want managed false", otel.LogUserPrompt)
	}
	if otel.LogAgentResponses == nil || !*otel.LogAgentResponses {
		t.Fatalf("log_agent_responses = %#v, want managed true", otel.LogAgentResponses)
	}
	if otel.LogGuardianAssessments == nil || !*otel.LogGuardianAssessments {
		t.Fatalf("log_guardian_assessments = %#v, want managed true", otel.LogGuardianAssessments)
	}
	if otel.Environment == nil || *otel.Environment != "managed" {
		t.Fatalf("environment = %#v", otel.Environment)
	}
	if otel.ToolResult == nil || otel.ToolResult.MaxBytes != 4096 {
		t.Fatalf("tool_result = %#v", otel.ToolResult)
	}
	if otel.Exporter == nil || otel.Exporter.Kind != OtelExporterKindOtlpHTTP ||
		otel.Exporter.Endpoint != "https://collector.example/v1/logs" ||
		otel.Exporter.Protocol != OtelHTTPProtocolJSON ||
		otel.Exporter.Headers["authorization"] != "Bearer managed" {
		t.Fatalf("exporter = %#v", otel.Exporter)
	}
	if otel.TraceExporter == nil || otel.TraceExporter.Kind != OtelExporterKindOtlpGRPC ||
		otel.TraceExporter.Endpoint != "https://collector.example:4317" {
		t.Fatalf("trace_exporter = %#v", otel.TraceExporter)
	}
	if otel.MetricsExporter == nil || otel.MetricsExporter.Kind != OtelExporterKindNone {
		t.Fatalf("metrics_exporter = %#v", otel.MetricsExporter)
	}
	if otel.SpanAttributes["deployment.environment"] != "managed" {
		t.Fatalf("span_attributes = %#v", otel.SpanAttributes)
	}
	if otel.Tracestate["acme"]["tenant"] != "acme" {
		t.Fatalf("tracestate = %#v", otel.Tracestate)
	}
}

// TestParseRequirementsTOMLRejectsInvalidOtelLikeRust pins the exporter
// validation Rust performs while deserializing OtelRequirementsToml.
func TestParseRequirementsTOMLRejectsInvalidOtelLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		document string
		want     string
	}{
		{
			name:     "unknown exporter variant",
			document: "[otel]\nexporter = \"bogus\"\n",
			want:     "unknown exporter variant",
		},
		{
			name: "unknown http protocol",
			document: `[otel.exporter."otlp-http"]
endpoint = "https://collector.example/v1/logs"
protocol = "protobuf"
`,
			want: "unknown protocol",
		},
		{
			name:     "negative tool result limit",
			document: "[otel.tool_result]\nmax_bytes = -1\n",
			want:     "non-negative integer",
		},
		{
			name:     "non-string span attribute",
			document: "[otel.span_attributes]\n\"deployment.environment\" = 1\n",
			want:     "must be a string",
		},
		{
			name:     "unknown field",
			document: "[otel]\nlog_user_prompts = true\n",
			want:     "otel: unknown field",
		},
		{
			name:     "non-boolean logging switch",
			document: "[otel]\nlog_user_prompt = \"yes\"\n",
			want:     "otel.log_user_prompt must be a boolean",
		},
		{
			name:     "non-table tool result",
			document: "[otel]\ntool_result = 5\n",
			want:     "otel.tool_result must be a table",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ParseRequirementsTOML([]byte(testCase.document)); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("ParseRequirementsTOML() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

// TestOtelRequirementTLSPathsResolveAgainstDocumentDirLikeRust covers Rust
// #57649: relative TLS paths in a requirements document resolve against the
// directory that owns the document.
func TestOtelRequirementTLSPathsResolveAgainstDocumentDirLikeRust(t *testing.T) {
	base := t.TempDir()
	requirements, err := ParseRequirementsTOMLWithBaseDir([]byte(`
[otel.trace_exporter."otlp-grpc"]
endpoint = "https://collector.example:4317"

[otel.trace_exporter."otlp-grpc".tls]
ca-certificate = "certs/ca.pem"
client-certificate = "certs/client.pem"
client-private-key = "certs/client.key"
`), base)
	if err != nil {
		t.Fatalf("ParseRequirementsTOMLWithBaseDir() error = %v", err)
	}
	if requirements == nil || requirements.Otel == nil || requirements.Otel.TraceExporter == nil || requirements.Otel.TraceExporter.TLS == nil {
		t.Fatalf("requirements.Otel = %#v", requirements)
	}
	tls := requirements.Otel.TraceExporter.TLS
	for name, testCase := range map[string]struct {
		got  *string
		want string
	}{
		"ca-certificate":     {tls.CACertificate, filepath.Join(base, "certs", "ca.pem")},
		"client-certificate": {tls.ClientCertificate, filepath.Join(base, "certs", "client.pem")},
		"client-private-key": {tls.ClientPrivateKey, filepath.Join(base, "certs", "client.key")},
	} {
		if testCase.got == nil || *testCase.got != testCase.want {
			t.Fatalf("%s = %#v, want %q", name, testCase.got, testCase.want)
		}
	}
}

// TestOtelRequirementsEmptyMetadataImposesNothingLikeRust covers Rust's
// OtelRequirementsToml::is_empty: empty metadata maps are not requirements.
func TestOtelRequirementsEmptyMetadataImposesNothingLikeRust(t *testing.T) {
	for _, document := range []string{
		"[otel]\n",
		"[otel.span_attributes]\n",
		"[otel.tracestate.empty]\n",
	} {
		requirements, err := ParseRequirementsTOML([]byte(document))
		if err != nil {
			t.Fatalf("ParseRequirementsTOML(%q) error = %v", document, err)
		}
		if requirements != nil && requirements.Otel != nil {
			t.Fatalf("ParseRequirementsTOML(%q).Otel = %#v, want no requirement", document, requirements.Otel)
		}
	}
}

// TestManagedOtelRequirementsOverrideUserConfigLikeRust covers Rust's
// apply_otel_requirements: managed exact values replace configured ones,
// exporters are replaced atomically and unmanaged span attributes survive.
func TestManagedOtelRequirementsOverrideUserConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `
[otel]
log_user_prompt = true
environment = "user"

[otel.span_attributes]
"user.attr" = "kept"

[otel.exporter."otlp-http"]
endpoint = "https://user.example/v1/logs"
protocol = "binary"
`)
	requirements := `
[otel]
log_user_prompt = false
environment = "managed"

[otel.span_attributes]
"managed.attr" = "set"

[otel.exporter."otlp-http"]
endpoint = "https://managed.example/v1/logs"
protocol = "json"

[otel.exporter."otlp-http".headers]
authorization = "Bearer managed"
`
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadEffectiveWithOptions(home, &EffectiveOptions{})
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	resolved := cfg.Otel()
	if resolved.LogUserPrompt {
		t.Fatal("log_user_prompt = true, want the managed false override")
	}
	if resolved.Environment != "managed" {
		t.Fatalf("environment = %q, want managed", resolved.Environment)
	}
	if resolved.Exporter.Kind != OtelExporterKindOtlpHTTP ||
		resolved.Exporter.Endpoint != "https://managed.example/v1/logs" ||
		resolved.Exporter.Protocol != OtelHTTPProtocolJSON {
		t.Fatalf("exporter = %#v, want the managed exporter", resolved.Exporter)
	}
	if resolved.Exporter.Headers["authorization"] != "Bearer managed" {
		t.Fatalf("exporter headers = %#v", resolved.Exporter.Headers)
	}
	if resolved.SpanAttributes["user.attr"] != "kept" || resolved.SpanAttributes["managed.attr"] != "set" {
		t.Fatalf("span attributes = %#v, want managed entries merged over unmanaged ones", resolved.SpanAttributes)
	}
}

// TestManagedOtelRequirementsWarnOnConflictLikeRust pins the single structured
// startup warning Rust emits when a managed OTEL value replaces a conflicting
// configured value.
func TestManagedOtelRequirementsWarnOnConflictLikeRust(t *testing.T) {
	logUserPrompt := false
	requirements := &ConfigRequirements{Otel: &OtelRequirements{LogUserPrompt: &logUserPrompt}}
	conflict := StartupWarnings(map[string]any{
		"otel": map[string]any{"log_user_prompt": true},
	}, requirements)
	want := "Configured values under `otel` are overridden by requirements from managed requirements."
	if len(conflict) != 1 || conflict[0] != want {
		t.Fatalf("StartupWarnings() = %#v, want %q", conflict, want)
	}
	agreeing := StartupWarnings(map[string]any{
		"otel": map[string]any{"log_user_prompt": false},
	}, requirements)
	if len(agreeing) != 0 {
		t.Fatalf("StartupWarnings() = %#v, want no warning for matching values", agreeing)
	}
	unset := StartupWarnings(map[string]any{}, requirements)
	if len(unset) != 0 {
		t.Fatalf("StartupWarnings() = %#v, want no warning without a configured value", unset)
	}
}

// TestManagedOtelRequirementsWriteRejectedLikeRust covers Rust #57658: managed
// OTEL fields are read-only through config writes while unmanaged siblings and
// unmanaged metadata entries stay writable.
func TestManagedOtelRequirementsWriteRejectedLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "[otel]\n")
	requirements := `
[otel]
log_user_prompt = false
tracestate = { managed = { tenant = "enterprise" }, empty = {} }

[otel.span_attributes]
"managed.attr" = "set"
`
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := LoadRequirementsFile(filepath.Join(home, "requirements.toml"))
	if err != nil {
		t.Fatal(err)
	}
	service := NewConfigService(home)
	service.SetRequirements(parsed)

	for _, keyPath := range []string{"otel.log_user_prompt", `otel.span_attributes."managed.attr"`, "otel.tracestate.managed.tenant"} {
		_, err := service.WriteValue(&ConfigValueWriteParams{KeyPath: keyPath, Value: "changed", MergeStrategy: MergeReplace})
		var writeErr *ConfigWriteError
		if !errors.As(err, &writeErr) || writeErr.Code != ConfigWriteRequirementReadonly {
			t.Fatalf("WriteValue(%s) error = %v, want configRequirementReadonly", keyPath, err)
		}
	}
	for _, keyPath := range []string{"otel.span_attributes.\"unmanaged.attr\"", "otel.tracestate.managed.region", "otel.tracestate.empty.tenant", "otel.environment"} {
		if _, err := service.WriteValue(&ConfigValueWriteParams{KeyPath: keyPath, Value: "value", MergeStrategy: MergeReplace}); err != nil {
			t.Fatalf("WriteValue(%s) error = %v, want unmanaged keys writable", keyPath, err)
		}
	}
}

// TestOtelWriteOverrideMetadataRedactsHeaderValuesLikeRust covers Rust #57654's
// write-override path: a managed exporter shadows the user's write, and the
// reported effective value is the redacted credential rather than the stored
// header value. Header names containing dots stay addressable.
func TestOtelWriteOverrideMetadataRedactsHeaderValuesLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "[otel]\n")
	service := NewConfigService(home)
	service.SetManagedLayers([]Layer{{
		Name:    LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: filepath.Join(home, "managed_config.toml")},
		Version: "managed-v1",
		Config: map[string]any{"otel": map[string]any{
			"exporter": map[string]any{"otlp-http": map[string]any{
				"endpoint": "https://collector.example/v1/logs",
				"protocol": "binary",
				"headers":  map[string]any{"x.tenant.authorization": "Bearer managed"},
			}},
		}},
	}})
	response, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:       `otel.exporter."otlp-http".headers."x.tenant.authorization"`,
		Value:         "Bearer user",
		MergeStrategy: MergeReplace,
	})
	if err != nil {
		t.Fatalf("WriteValue() error = %v", err)
	}
	if response.Status != WriteOKOverridden || response.OverriddenMetadata == nil {
		t.Fatalf("response = %+v, want an overridden write", response)
	}
	if got := response.OverriddenMetadata.EffectiveValue; got != "[redacted]" {
		t.Fatalf("effective value = %#v, want the redacted header value", got)
	}
}

// TestOtelRequirementsMarshalRedactsHeadersLikeRust covers Rust #57654: the
// configRequirements/read projection masks exporter header values while the
// in-process requirements keep the raw credentials.
func TestOtelRequirementsMarshalRedactsHeadersLikeRust(t *testing.T) {
	requirements := &ConfigRequirements{Otel: &OtelRequirements{
		Exporter: &OtelExporter{
			Kind:     OtelExporterKindOtlpHTTP,
			Endpoint: "https://collector.example/v1/logs",
			Protocol: OtelHTTPProtocolBinary,
			Headers:  map[string]string{"authorization": "Bearer secret", "x-tenant": "acme"},
		},
	}}
	data, err := json.Marshal(requirements)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(data), "Bearer secret") || strings.Contains(string(data), "acme") {
		t.Fatalf("marshalled requirements leaked header values: %s", data)
	}
	if !strings.Contains(string(data), "[redacted]") {
		t.Fatalf("marshalled requirements omitted redaction: %s", data)
	}
	if requirements.Otel.Exporter.Headers["authorization"] != "Bearer secret" {
		t.Fatalf("marshalling mutated the raw requirements: %#v", requirements.Otel.Exporter.Headers)
	}
}

// TestMergeOtelRequirementsKeepsLowerMetadataLikeRust covers Rust's
// merge_otel_requirements: higher-precedence exact values win while unmanaged
// span attributes and tracestate members from the lower layer survive.
func TestMergeOtelRequirementsKeepsLowerMetadataLikeRust(t *testing.T) {
	lower := &OtelRequirements{
		LogUserPrompt:  boolPtr(false),
		SpanAttributes: map[string]string{"lower": "kept", "shared": "lower"},
		Tracestate:     map[string]map[string]string{"acme": {"tenant": "lower"}},
	}
	merged := mergeOtelRequirements(lower, &OtelRequirements{
		Environment:    stringPtr("managed"),
		SpanAttributes: map[string]string{"shared": "higher"},
		Tracestate:     map[string]map[string]string{"acme": {"tenant": "higher", "region": "eu"}},
	})
	if merged == nil {
		t.Fatal("mergeOtelRequirements() = nil")
	}
	if merged.LogUserPrompt == nil || *merged.LogUserPrompt {
		t.Fatalf("log_user_prompt = %#v, want the lower layer value", merged.LogUserPrompt)
	}
	if merged.Environment == nil || *merged.Environment != "managed" {
		t.Fatalf("environment = %#v", merged.Environment)
	}
	if !reflect.DeepEqual(merged.SpanAttributes, map[string]string{"lower": "kept", "shared": "higher"}) {
		t.Fatalf("span attributes = %#v", merged.SpanAttributes)
	}
	if !reflect.DeepEqual(merged.Tracestate["acme"], map[string]string{"tenant": "higher", "region": "eu"}) {
		t.Fatalf("tracestate = %#v", merged.Tracestate)
	}
	if lower.SpanAttributes["shared"] != "lower" || lower.Tracestate["acme"]["tenant"] != "lower" {
		t.Fatalf("merge mutated the lower layer: %#v", lower)
	}
}

// TestRedactOtelExporterHeadersMasksEveryExporter covers the app-server
// boundary helper: every exporter table is masked in place while header names
// and unrelated config survive.
func TestRedactOtelExporterHeadersMasksEveryExporter(t *testing.T) {
	values := map[string]any{
		"otel": map[string]any{
			"environment": "managed",
			"exporter": map[string]any{"otlp-http": map[string]any{
				"endpoint": "https://collector.example/v1/logs",
				"headers":  map[string]any{"authorization": "Bearer logs"},
			}},
			"trace_exporter": map[string]any{"otlp-grpc": map[string]any{
				"endpoint": "https://collector.example:4317",
				"headers":  map[string]any{"authorization": "Bearer traces"},
			}},
			"metrics_exporter": map[string]any{"otlp-grpc": map[string]any{
				"endpoint": "https://collector.example:4317",
				"headers":  map[string]any{"authorization": "Bearer metrics"},
			}},
		},
	}
	RedactOtelExporterHeaders(values["otel"])
	otel, _ := values["otel"].(map[string]any)
	for _, field := range []string{"exporter", "trace_exporter", "metrics_exporter"} {
		exporter, _ := otel[field].(map[string]any)
		for _, raw := range exporter {
			body, _ := raw.(map[string]any)
			headers, _ := body["headers"].(map[string]any)
			if headers["authorization"] != "[redacted]" {
				t.Fatalf("%s headers = %#v, want redacted values", field, headers)
			}
			if _, ok := headers["authorization"]; !ok {
				t.Fatalf("%s dropped the header name", field)
			}
		}
	}
	if otel["environment"] != "managed" {
		t.Fatalf("redaction changed unrelated settings: %#v", otel)
	}
}

func boolPtr(value bool) *bool {
	return &value
}
