package telemetry

import (
	"testing"

	"codex_go/state"
)

// Rust RuntimeMetricsSummary::from_snapshot: counters sum their increments and
// durations sum their histogram values, matched by the metric names.
func TestRuntimeMetricsSummaryFromRecordsLikeRust(t *testing.T) {
	records := []*state.TaskMetric{
		{Name: ToolCallCountMetric, Inc: 3},
		{Name: ToolCallDurationMetric, DurationMS: 120},
		{Name: ToolCallDurationMetric, DurationMS: 30},
		{Name: APICallCountMetric, Inc: 2},
		{Name: APICallDurationMetric, DurationMS: 500},
		{Name: SSEEventCountMetric, Inc: 11},
		{Name: SSEEventDurationMetric, DurationMS: 12.5},
		{Name: WebSocketRequestCountMetric, Inc: 1},
		{Name: WebSocketRequestDurationMetric, DurationMS: 40},
		{Name: WebSocketEventCountMetric, Inc: 5},
		{Name: WebSocketEventDurationMetric, DurationMS: 7},
		{Name: ResponsesAPIOverheadDurationMetric, DurationMS: 9},
		{Name: ResponsesAPIInferenceTimeDurationMetric, DurationMS: 900},
		{Name: ResponsesAPIEngineIAPITTFTDurationMetric, DurationMS: 60},
		{Name: ResponsesAPIEngineServiceTTFTDurationMetric, DurationMS: 70},
		{Name: ResponsesAPIEngineIAPITBTDurationMetric, DurationMS: 1.5},
		{Name: ResponsesAPIEngineServiceTBTDurationMetric, DurationMS: 2.5},
		{Name: TurnTTFTDurationMetric, DurationMS: 300},
		{Name: TurnTTFMDurationMetric, DurationMS: 400},
		{Name: "unrelated.metric", Inc: 99, DurationMS: 99},
		nil,
	}
	summary := RuntimeMetricsSummaryFromRecords(records)
	if summary.ToolCalls.Count != 3 || summary.ToolCalls.DurationMS != 150 {
		t.Fatalf("tool calls = %#v", summary.ToolCalls)
	}
	if summary.APICalls.Count != 2 || summary.APICalls.DurationMS != 500 {
		t.Fatalf("api calls = %#v", summary.APICalls)
	}
	if summary.StreamingEvents.Count != 11 || summary.StreamingEvents.DurationMS != 13 {
		t.Fatalf("streaming events = %#v", summary.StreamingEvents)
	}
	if summary.WebSocketCalls.Count != 1 || summary.WebSocketCalls.DurationMS != 40 {
		t.Fatalf("websocket calls = %#v", summary.WebSocketCalls)
	}
	if summary.WebSocketEvents.Count != 5 || summary.WebSocketEvents.DurationMS != 7 {
		t.Fatalf("websocket events = %#v", summary.WebSocketEvents)
	}
	if summary.ResponsesAPIOverheadMS != 9 || summary.ResponsesAPIInferenceTimeMS != 900 ||
		summary.ResponsesAPIEngineIAPITTFTMS != 60 || summary.ResponsesAPIEngineServiceTTFTMS != 70 ||
		summary.ResponsesAPIEngineIAPITBTMS != 1.5 || summary.ResponsesAPIEngineServiceTBTMS != 2.5 ||
		summary.TurnTTFTMS != 300 || summary.TurnTTFMMS != 400 {
		t.Fatalf("timings = %#v", summary)
	}
	if summary.IsEmpty() {
		t.Fatal("a populated summary reported empty")
	}
	if !RuntimeMetricsSummaryFromRecords(nil).IsEmpty() || !RuntimeMetricsSummaryFromRecords([]*state.TaskMetric{{Name: "other", Inc: 5}}).IsEmpty() {
		t.Fatal("an empty record set produced totals")
	}
}

// The delta of two cumulative snapshots is what Rust's manual reader drains, and
// merging deltas accumulates like the TUI's turn metrics.
func TestRuntimeMetricsSummaryDeltasLikeRust(t *testing.T) {
	first := RuntimeMetricsSummaryFromRecords([]*state.TaskMetric{
		{Name: ToolCallCountMetric, Inc: 1},
		{Name: ToolCallDurationMetric, DurationMS: 10},
		{Name: TurnTTFTDurationMetric, DurationMS: 100},
	})
	second := RuntimeMetricsSummaryFromRecords([]*state.TaskMetric{
		{Name: ToolCallCountMetric, Inc: 3},
		{Name: ToolCallDurationMetric, DurationMS: 50},
		{Name: TurnTTFTDurationMetric, DurationMS: 250},
	})
	delta := second.Subtract(first)
	if delta.ToolCalls.Count != 2 || delta.ToolCalls.DurationMS != 40 || delta.TurnTTFTMS != 150 {
		t.Fatalf("delta = %#v", delta)
	}
	if !first.Subtract(second).IsEmpty() {
		t.Fatalf("a shrinking snapshot must clamp at zero: %#v", first.Subtract(second))
	}

	merged := RuntimeMetricsSummary{}
	merged.Merge(first)
	merged.Merge(delta)
	if merged.ToolCalls.Count != 3 || merged.ToolCalls.DurationMS != 50 {
		t.Fatalf("merged totals = %#v", merged.ToolCalls)
	}
	if merged.TurnTTFTMS != 150 {
		t.Fatalf("merged turn ttft = %d, want the latest delta value", merged.TurnTTFTMS)
	}
}
