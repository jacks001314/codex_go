package config

import "testing"

// Mirrors Rust #58034: `otel.log_sse_deltas` is a managed OTEL requirement that
// defaults to true, can be disabled, and overrides user configuration.
func TestOtelSSEDeltaRequirementsLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte("[otel]\nlog_sse_deltas = false\n"))
	if err != nil {
		t.Fatalf("parse requirements: %v", err)
	}
	if requirements == nil || requirements.Otel == nil || requirements.Otel.LogSSEDeltas == nil || *requirements.Otel.LogSSEDeltas {
		t.Fatalf("requirements = %#v", requirements)
	}

	values := map[string]any{"otel": map[string]any{"log_sse_deltas": true}}
	applyManagedOtelOverrides(values, requirements)
	otel, _ := values["otel"].(map[string]any)
	if otel["log_sse_deltas"] != false {
		t.Fatalf("managed override = %#v", otel)
	}
	if !otelRequirementsConflict(map[string]any{"otel": map[string]any{"log_sse_deltas": true}}, requirements.Otel) {
		t.Fatal("a user value that differs from the managed one must conflict")
	}
	if path := otelRequirementForConfigPath(requirements.Otel, []string{"otel", "log_sse_deltas"}); path != "otel.log_sse_deltas" {
		t.Fatalf("managed path = %q", path)
	}

	if !DefaultOtelConfig().LogSSEDeltas {
		t.Fatal("log_sse_deltas must default to true")
	}
	resolved, _ := ResolveOtelConfig(values)
	if resolved.LogSSEDeltas {
		t.Fatalf("resolved config = %#v, want the managed false value", resolved)
	}

	if _, err := otelRequirementsFromMap(map[string]any{"log_sse_deltas": "no"}); err == nil {
		t.Fatal("a non-boolean log_sse_deltas must be rejected")
	}
	merged := mergeOtelRequirements(
		&OtelRequirements{},
		&OtelRequirements{LogSSEDeltas: boolPtrForTest(false)},
	)
	if merged == nil || merged.LogSSEDeltas == nil || *merged.LogSSEDeltas {
		t.Fatalf("merged requirements = %#v", merged)
	}
}

func boolPtrForTest(value bool) *bool { return &value }
