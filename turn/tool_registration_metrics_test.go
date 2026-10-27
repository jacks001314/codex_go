package turn

import (
	"reflect"
	"testing"

	"codex_go/metrics"
	"codex_go/model"
	"codex_go/tool"
)

type recordedToolRegistrationMetric struct {
	name       string
	value      int
	boundaries []float64
	tags       map[string]string
}

type recordingToolRegistrationMetricSink struct {
	samples []recordedToolRegistrationMetric
}

func (s *recordingToolRegistrationMetricSink) HistogramWithBounds(name string, value int, boundaries []float64, tags map[string]string) {
	clonedTags := make(map[string]string, len(tags))
	for key, value := range tags {
		clonedTags[key] = value
	}
	s.samples = append(s.samples, recordedToolRegistrationMetric{
		name:       name,
		value:      value,
		boundaries: append([]float64(nil), boundaries...),
		tags:       clonedTags,
	})
}

func TestRecordToolRegistrationMetricsIncludesEmptyExposuresLikeRust(t *testing.T) {
	sink := &recordingToolRegistrationMetricSink{}
	recordToolRegistrationMetrics(sink, tool.RegistrationCounts{}, model.ToolModeCodeMode, false)

	if len(sink.samples) != 6 {
		t.Fatalf("samples = %d, want 6", len(sink.samples))
	}
	wantExposures := []string{
		"direct",
		"direct_model_only",
		"deferred",
		"deferred_model_only",
		"code_mode_only",
		"hidden",
	}
	for index, sample := range sink.samples {
		if sample.name != metrics.ToolRegistrationsMetric || sample.value != 0 {
			t.Fatalf("sample[%d] = %#v, want zero-valued %s", index, sample, metrics.ToolRegistrationsMetric)
		}
		if !reflect.DeepEqual(sample.boundaries, metrics.ToolRegistrationsMetricBoundaries) {
			t.Fatalf("sample[%d] boundaries = %#v, want %#v", index, sample.boundaries, metrics.ToolRegistrationsMetricBoundaries)
		}
		wantTags := map[string]string{
			"exposure":          wantExposures[index],
			"tool_mode":         model.ToolModeCodeMode,
			"incremental_tools": "false",
		}
		if !reflect.DeepEqual(sample.tags, wantTags) {
			t.Fatalf("sample[%d] tags = %#v, want %#v", index, sample.tags, wantTags)
		}
	}
}
