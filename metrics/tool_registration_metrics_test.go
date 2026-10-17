package metrics

import (
	"reflect"
	"testing"
)

func TestToolRegistrationMetricsMatchRust(t *testing.T) {
	if ToolRegistrationsMetric != "codex.tools.registered" {
		t.Fatalf("ToolRegistrationsMetric = %q, want %q", ToolRegistrationsMetric, "codex.tools.registered")
	}
	want := []float64{
		0, 1, 2, 5, 10, 20, 30, 50, 75, 100,
		150, 200, 300, 500, 750, 1_000, 2_000, 5_000, 10_000, 20_000,
	}
	if !reflect.DeepEqual(ToolRegistrationsMetricBoundaries, want) {
		t.Fatalf("ToolRegistrationsMetricBoundaries = %#v, want %#v", ToolRegistrationsMetricBoundaries, want)
	}
}
