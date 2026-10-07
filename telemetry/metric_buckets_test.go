package telemetry

import (
	"context"
	"strconv"
	"testing"
)

// Mirrors Rust #48819 (codex-rs/otel/tests/suite/send.rs
// `context_histograms_preserve_zero_and_family_ranges`): the context-budget
// histograms keep zero in its own bucket, stay strictly increasing, end at the
// family maximum, and retain an overflow bucket above it.
func TestContextMetricBoundariesPreserveZeroAndFamilyRanges(t *testing.T) {
	cases := []struct {
		name       string
		boundaries []float64
		maximum    int
	}{
		{ThreadSkillsEnabledTotalMetric, ThreadSkillsCountMetricBoundaries, 512},
		{ThreadSkillsKeptTotalMetric, ThreadSkillsCountMetricBoundaries, 512},
		{ThreadSkillsDescriptionTruncatedCharsMetric, ThreadSkillsDescriptionTruncatedCharsBoundaries, 131_072},
		{ThreadSkillsTruncatedMetric, ThreadSkillsTruncatedBoundaries, 1},
	}

	doer := &recordingHTTPDoer{}
	client := newTestMetricsClient(t, doer, MetricsClientOptions{})
	if !client.Enabled() {
		t.Fatal("client is disabled")
	}
	for _, testCase := range cases {
		for _, value := range []int{0, 1, testCase.maximum, testCase.maximum + 1} {
			client.HistogramWithBounds(testCase.name, value, testCase.boundaries, nil)
		}
	}
	if err := client.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	requests := doer.snapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	metrics := exportedMetricsByName(t, requests[0].body)
	for _, testCase := range cases {
		boundaries := testCase.boundaries
		if len(boundaries) == 0 {
			t.Fatalf("%s has no explicit boundaries", testCase.name)
		}
		if boundaries[0] != 0 || boundaries[1] != 1 {
			t.Fatalf("%s boundaries start = %v, want [0 1]", testCase.name, boundaries[:2])
		}
		if boundaries[len(boundaries)-1] != float64(testCase.maximum) {
			t.Fatalf("%s last boundary = %v, want %d", testCase.name, boundaries[len(boundaries)-1], testCase.maximum)
		}
		for index := 1; index < len(boundaries); index++ {
			if boundaries[index-1] >= boundaries[index] {
				t.Fatalf("%s boundaries not strictly increasing at %d: %v", testCase.name, index, boundaries[index-1:index+1])
			}
		}

		histogram := metrics[testCase.name]["histogram"].(map[string]any)
		point := histogram["dataPoints"].([]any)[0].(map[string]any)
		exportedBounds := point["explicitBounds"].([]any)
		if len(exportedBounds) != len(boundaries) {
			t.Fatalf("%s exported bounds = %d, want %d", testCase.name, len(exportedBounds), len(boundaries))
		}
		for index, want := range boundaries {
			if exportedBounds[index].(float64) != want {
				t.Fatalf("%s exported bounds[%d] = %v, want %v", testCase.name, index, exportedBounds[index], want)
			}
		}
		counts := point["bucketCounts"].([]any)
		if len(counts) != len(boundaries)+1 {
			t.Fatalf("%s bucket counts = %d, want %d", testCase.name, len(counts), len(boundaries)+1)
		}
		// 0, 1, the maximum, and the overflow above it land in the first, second,
		// last bounded and overflow buckets; a family whose maximum is 1 puts
		// both 1 and the flag into the same bucket.
		expectedCounts := make([]int, len(boundaries)+1)
		for _, index := range []int{0, 1, len(boundaries) - 1, len(boundaries)} {
			expectedCounts[index]++
		}
		for index, want := range expectedCounts {
			if counts[index] != strconv.Itoa(want) {
				t.Fatalf("%s bucketCounts = %v, want %v", testCase.name, counts, expectedCounts)
			}
		}
		if point["count"] != "4" {
			t.Fatalf("%s sample count = %v, want 4", testCase.name, point["count"])
		}
		wantSum := float64(0 + 1 + testCase.maximum + testCase.maximum + 1)
		if point["sum"].(float64) != wantSum {
			t.Fatalf("%s sample sum = %v, want %v", testCase.name, point["sum"], wantSum)
		}
	}
}

// exportedMetricsByName indexes one OTLP/JSON request body the way the other
// exporter tests in this package do.
func exportedMetricsByName(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	resourceMetrics := body["resourceMetrics"].([]any)
	scopeMetrics := resourceMetrics[0].(map[string]any)["scopeMetrics"].([]any)
	metrics := map[string]map[string]any{}
	for _, raw := range scopeMetrics[0].(map[string]any)["metrics"].([]any) {
		metric := raw.(map[string]any)
		metrics[metric["name"].(string)] = metric
	}
	return metrics
}
