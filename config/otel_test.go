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
