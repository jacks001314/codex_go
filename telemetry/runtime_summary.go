package telemetry

import (
	"math"

	"codex_go/state"
)

// Rust parity: codex-rs/otel/src/metrics/runtime_metrics.rs. Rust's
// `runtime_metrics` feature installs a manual reader over the metrics it already
// recorded and projects the drained snapshot into a RuntimeMetricsSummary; the
// TUI's chatwidget merges those deltas and reports the totals with the turn's
// completion metadata.

// RuntimeMetricTotals is one count/duration pair (Rust RuntimeMetricTotals).
type RuntimeMetricTotals struct {
	Count      uint64 `json:"count"`
	DurationMS uint64 `json:"duration_ms"`
}

// IsEmpty reports whether neither the count nor the duration was recorded.
func (t RuntimeMetricTotals) IsEmpty() bool {
	return t.Count == 0 && t.DurationMS == 0
}

// Merge adds another pair, saturating like Rust's saturating_add.
func (t *RuntimeMetricTotals) Merge(other RuntimeMetricTotals) {
	if t == nil {
		return
	}
	t.Count = saturatingAddUint64(t.Count, other.Count)
	t.DurationMS = saturatingAddUint64(t.DurationMS, other.DurationMS)
}

// Subtract removes an earlier snapshot's totals from this one, clamping at zero
// so a caller can turn cumulative records into the deltas Rust's reader drains.
func (t RuntimeMetricTotals) Subtract(previous RuntimeMetricTotals) RuntimeMetricTotals {
	return RuntimeMetricTotals{
		Count:      subtractUint64(t.Count, previous.Count),
		DurationMS: subtractUint64(t.DurationMS, previous.DurationMS),
	}
}

// RuntimeMetricsSummary mirrors Rust's RuntimeMetricsSummary: the recorded
// tool/api/streaming/websocket totals plus the responses-api and turn timings.
type RuntimeMetricsSummary struct {
	ToolCalls                       RuntimeMetricTotals `json:"tool_calls"`
	APICalls                        RuntimeMetricTotals `json:"api_calls"`
	StreamingEvents                 RuntimeMetricTotals `json:"streaming_events"`
	WebSocketCalls                  RuntimeMetricTotals `json:"websocket_calls"`
	WebSocketEvents                 RuntimeMetricTotals `json:"websocket_events"`
	ResponsesAPIOverheadMS          uint64              `json:"responses_api_overhead_ms"`
	ResponsesAPIInferenceTimeMS     uint64              `json:"responses_api_inference_time_ms"`
	ResponsesAPIEngineIAPITTFTMS    uint64              `json:"responses_api_engine_iapi_ttft_ms"`
	ResponsesAPIEngineServiceTTFTMS uint64              `json:"responses_api_engine_service_ttft_ms"`
	ResponsesAPIEngineIAPITBTMS     float64             `json:"responses_api_engine_iapi_tbt_ms"`
	ResponsesAPIEngineServiceTBTMS  float64             `json:"responses_api_engine_service_tbt_ms"`
	TurnTTFTMS                      uint64              `json:"turn_ttft_ms"`
	TurnTTFMMS                      uint64              `json:"turn_ttfm_ms"`
}

// IsEmpty reports whether nothing was recorded, matching Rust's is_empty.
func (s RuntimeMetricsSummary) IsEmpty() bool {
	return s.ToolCalls.IsEmpty() &&
		s.APICalls.IsEmpty() &&
		s.StreamingEvents.IsEmpty() &&
		s.WebSocketCalls.IsEmpty() &&
		s.WebSocketEvents.IsEmpty() &&
		s.ResponsesAPIOverheadMS == 0 &&
		s.ResponsesAPIInferenceTimeMS == 0 &&
		s.ResponsesAPIEngineIAPITTFTMS == 0 &&
		s.ResponsesAPIEngineServiceTTFTMS == 0 &&
		s.ResponsesAPIEngineIAPITBTMS == 0 &&
		s.ResponsesAPIEngineServiceTBTMS == 0 &&
		s.TurnTTFTMS == 0 &&
		s.TurnTTFMMS == 0
}

// Merge folds another summary into this one (Rust RuntimeMetricsSummary::merge):
// totals add, and a later non-zero timing replaces the earlier value.
func (s *RuntimeMetricsSummary) Merge(other RuntimeMetricsSummary) {
	if s == nil {
		return
	}
	s.ToolCalls.Merge(other.ToolCalls)
	s.APICalls.Merge(other.APICalls)
	s.StreamingEvents.Merge(other.StreamingEvents)
	s.WebSocketCalls.Merge(other.WebSocketCalls)
	s.WebSocketEvents.Merge(other.WebSocketEvents)
	if other.ResponsesAPIOverheadMS > 0 {
		s.ResponsesAPIOverheadMS = other.ResponsesAPIOverheadMS
	}
	if other.ResponsesAPIInferenceTimeMS > 0 {
		s.ResponsesAPIInferenceTimeMS = other.ResponsesAPIInferenceTimeMS
	}
	if other.ResponsesAPIEngineIAPITTFTMS > 0 {
		s.ResponsesAPIEngineIAPITTFTMS = other.ResponsesAPIEngineIAPITTFTMS
	}
	if other.ResponsesAPIEngineServiceTTFTMS > 0 {
		s.ResponsesAPIEngineServiceTTFTMS = other.ResponsesAPIEngineServiceTTFTMS
	}
	if other.ResponsesAPIEngineIAPITBTMS > 0 {
		s.ResponsesAPIEngineIAPITBTMS = other.ResponsesAPIEngineIAPITBTMS
	}
	if other.ResponsesAPIEngineServiceTBTMS > 0 {
		s.ResponsesAPIEngineServiceTBTMS = other.ResponsesAPIEngineServiceTBTMS
	}
	if other.TurnTTFTMS > 0 {
		s.TurnTTFTMS = other.TurnTTFTMS
	}
	if other.TurnTTFMMS > 0 {
		s.TurnTTFMMS = other.TurnTTFMMS
	}
}

// Subtract removes an earlier cumulative snapshot, producing the delta a manual
// reader would have drained (the count/duration totals and every timing field).
func (s RuntimeMetricsSummary) Subtract(previous RuntimeMetricsSummary) RuntimeMetricsSummary {
	return RuntimeMetricsSummary{
		ToolCalls:                       s.ToolCalls.Subtract(previous.ToolCalls),
		APICalls:                        s.APICalls.Subtract(previous.APICalls),
		StreamingEvents:                 s.StreamingEvents.Subtract(previous.StreamingEvents),
		WebSocketCalls:                  s.WebSocketCalls.Subtract(previous.WebSocketCalls),
		WebSocketEvents:                 s.WebSocketEvents.Subtract(previous.WebSocketEvents),
		ResponsesAPIOverheadMS:          subtractUint64(s.ResponsesAPIOverheadMS, previous.ResponsesAPIOverheadMS),
		ResponsesAPIInferenceTimeMS:     subtractUint64(s.ResponsesAPIInferenceTimeMS, previous.ResponsesAPIInferenceTimeMS),
		ResponsesAPIEngineIAPITTFTMS:    subtractUint64(s.ResponsesAPIEngineIAPITTFTMS, previous.ResponsesAPIEngineIAPITTFTMS),
		ResponsesAPIEngineServiceTTFTMS: subtractUint64(s.ResponsesAPIEngineServiceTTFTMS, previous.ResponsesAPIEngineServiceTTFTMS),
		ResponsesAPIEngineIAPITBTMS:     math.Max(0, s.ResponsesAPIEngineIAPITBTMS-previous.ResponsesAPIEngineIAPITBTMS),
		ResponsesAPIEngineServiceTBTMS:  math.Max(0, s.ResponsesAPIEngineServiceTBTMS-previous.ResponsesAPIEngineServiceTBTMS),
		TurnTTFTMS:                      subtractUint64(s.TurnTTFTMS, previous.TurnTTFTMS),
		TurnTTFMMS:                      subtractUint64(s.TurnTTFMMS, previous.TurnTTFMMS),
	}
}

// RuntimeMetricsSummaryFromRecords projects the recorded metrics into Rust's
// summary: counters sum their increments and histograms/durations sum their
// values, matched by metric name.
func RuntimeMetricsSummaryFromRecords(records []*state.TaskMetric) RuntimeMetricsSummary {
	var summary RuntimeMetricsSummary
	var toolDuration, apiDuration, sseDuration float64
	var websocketCallDuration, websocketEventDuration float64
	for _, record := range records {
		if record == nil {
			continue
		}
		name := record.Name
		switch name {
		case ToolCallCountMetric:
			summary.ToolCalls.Count = saturatingAddUint64(summary.ToolCalls.Count, uint64(nonNegativeInt(record.Inc)))
		case ToolCallDurationMetric:
			toolDuration += nonNegativeFloat(record.DurationMS)
		case APICallCountMetric:
			summary.APICalls.Count = saturatingAddUint64(summary.APICalls.Count, uint64(nonNegativeInt(record.Inc)))
		case APICallDurationMetric:
			apiDuration += nonNegativeFloat(record.DurationMS)
		case SSEEventCountMetric:
			summary.StreamingEvents.Count = saturatingAddUint64(summary.StreamingEvents.Count, uint64(nonNegativeInt(record.Inc)))
		case SSEEventDurationMetric:
			sseDuration += nonNegativeFloat(record.DurationMS)
		case WebSocketRequestCountMetric:
			summary.WebSocketCalls.Count = saturatingAddUint64(summary.WebSocketCalls.Count, uint64(nonNegativeInt(record.Inc)))
		case WebSocketRequestDurationMetric:
			websocketCallDuration += nonNegativeFloat(record.DurationMS)
		case WebSocketEventCountMetric:
			summary.WebSocketEvents.Count = saturatingAddUint64(summary.WebSocketEvents.Count, uint64(nonNegativeInt(record.Inc)))
		case WebSocketEventDurationMetric:
			websocketEventDuration += nonNegativeFloat(record.DurationMS)
		case ResponsesAPIOverheadDurationMetric:
			summary.ResponsesAPIOverheadMS = saturatingAddUint64(summary.ResponsesAPIOverheadMS, uint64(nonNegativeFloat(record.DurationMS)))
		case ResponsesAPIInferenceTimeDurationMetric:
			summary.ResponsesAPIInferenceTimeMS = saturatingAddUint64(summary.ResponsesAPIInferenceTimeMS, uint64(nonNegativeFloat(record.DurationMS)))
		case ResponsesAPIEngineIAPITTFTDurationMetric:
			summary.ResponsesAPIEngineIAPITTFTMS = saturatingAddUint64(summary.ResponsesAPIEngineIAPITTFTMS, uint64(nonNegativeFloat(record.DurationMS)))
		case ResponsesAPIEngineServiceTTFTDurationMetric:
			summary.ResponsesAPIEngineServiceTTFTMS = saturatingAddUint64(summary.ResponsesAPIEngineServiceTTFTMS, uint64(nonNegativeFloat(record.DurationMS)))
		case ResponsesAPIEngineIAPITBTDurationMetric:
			summary.ResponsesAPIEngineIAPITBTMS += nonNegativeFloat(record.DurationMS)
		case ResponsesAPIEngineServiceTBTDurationMetric:
			summary.ResponsesAPIEngineServiceTBTMS += nonNegativeFloat(record.DurationMS)
		case TurnTTFTDurationMetric:
			summary.TurnTTFTMS = saturatingAddUint64(summary.TurnTTFTMS, uint64(nonNegativeFloat(record.DurationMS)))
		case TurnTTFMDurationMetric:
			summary.TurnTTFMMS = saturatingAddUint64(summary.TurnTTFMMS, uint64(nonNegativeFloat(record.DurationMS)))
		}
	}
	summary.ToolCalls.DurationMS = uint64(math.Round(toolDuration))
	summary.APICalls.DurationMS = uint64(math.Round(apiDuration))
	summary.StreamingEvents.DurationMS = uint64(math.Round(sseDuration))
	summary.WebSocketCalls.DurationMS = uint64(math.Round(websocketCallDuration))
	summary.WebSocketEvents.DurationMS = uint64(math.Round(websocketEventDuration))
	return summary
}

func nonNegativeInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func nonNegativeFloat(value float64) float64 {
	if value < 0 || math.IsNaN(value) {
		return 0
	}
	return value
}

func saturatingAddUint64(left uint64, right uint64) uint64 {
	sum := left + right
	if sum < left {
		return math.MaxUint64
	}
	return sum
}

func subtractUint64(value uint64, previous uint64) uint64 {
	if previous >= value {
		return 0
	}
	return value - previous
}
