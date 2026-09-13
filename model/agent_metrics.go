package model

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Rust parity: codex-rs/core/src/client.rs's RequestTelemetry::on_request ->
// SessionTelemetry::record_api_request (codex-rs/otel/src/events/session_telemetry.rs).
// The model package cannot import codex_go/telemetry - telemetry reaches back
// here through memories - so the sink and the two metric names are declared
// locally; telemetry.TurnMetricSink and state.TaskMetrics satisfy MetricsSink
// structurally.

// MetricsSink receives the model client's request metrics.
type MetricsSink interface {
	Counter(name string, inc int, tags map[string]string)
	RecordDuration(name string, duration time.Duration, tags map[string]string)
}

// Metric names mirror codex-rs/otel/src/metrics/names.rs.
const (
	apiCallCountMetric                          = "codex.api_request"
	apiCallDurationMetric                       = "codex.api_request.duration_ms"
	sseEventCountMetric                         = "codex.sse_event"
	sseEventDurationMetric                      = "codex.sse_event.duration_ms"
	websocketEventCountMetric                   = "codex.websocket.event"
	websocketEventDurationMetric                = "codex.websocket.event.duration_ms"
	websocketRequestCountMetric                 = "codex.websocket.request"
	websocketRequestDurationMetric              = "codex.websocket.request.duration_ms"
	responsesAPIOverheadDurationMetric          = "codex.responses_api_overhead.duration_ms"
	responsesAPIInferenceTimeDurationMetric     = "codex.responses_api_inference_time.duration_ms"
	responsesAPIEngineIAPITTFTDurationMetric    = "codex.responses_api_engine_iapi_ttft.duration_ms"
	responsesAPIEngineServiceTTFTDurationMetric = "codex.responses_api_engine_service_ttft.duration_ms"
	responsesAPIEngineIAPITBTDurationMetric     = "codex.responses_api_engine_iapi_tbt.duration_ms"
	responsesAPIEngineServiceTBTDurationMetric  = "codex.responses_api_engine_service_tbt.duration_ms"
)

// sseUnknownKind / websocketUnknownKind mirror SSE_UNKNOWN_KIND /
// WEBSOCKET_UNKNOWN_KIND.
const (
	sseUnknownKind       = "unknown"
	websocketUnknownKind = "unknown"
)

// responsesWebsocketTimingKind mirrors RESPONSES_WEBSOCKET_TIMING_KIND.
const responsesWebsocketTimingKind = "responsesapi.websocket_timing"

// recordAPIRequest mirrors record_api_request's metric half: one counter and one
// millisecond duration histogram per HTTP attempt, tagged by the response status
// ("none" for a transport failure) and success.
func (r *ResponsesAgentRunner) recordAPIRequest(status int, err error, duration time.Duration) {
	if r == nil || r.Metrics == nil {
		return
	}
	success := err == nil && status >= 200 && status <= 299
	statusTag := "none"
	if status != 0 {
		statusTag = strconv.Itoa(status)
	}
	tags := map[string]string{"status": statusTag, "success": strconv.FormatBool(success)}
	r.Metrics.Counter(apiCallCountMetric, 1, tags)
	r.Metrics.RecordDuration(apiCallDurationMetric, duration, tags)
}

// recordSSEEvent mirrors SessionTelemetry's sse_event/sse_event_failed metric
// half: one counter and one millisecond duration histogram per processed SSE
// event, tagged by the event kind and success.
func recordSSEEvent(metrics MetricsSink, kind string, success bool, duration time.Duration) {
	if metrics == nil {
		return
	}
	if kind == "" {
		kind = sseUnknownKind
	}
	tags := map[string]string{"kind": kind, "success": strconv.FormatBool(success)}
	metrics.Counter(sseEventCountMetric, 1, tags)
	metrics.RecordDuration(sseEventDurationMetric, duration, tags)
}

// sseEventKind resolves the kind tag: the SSE event name, else the JSON type,
// else "unknown" (Rust passes the parsed event name and falls back to
// SSE_UNKNOWN_KIND only for events that never parsed).
func sseEventKind(sse *responsesSSEEvent) string {
	if sse == nil {
		return sseUnknownKind
	}
	if kind := strings.TrimSpace(sse.Event); kind != "" {
		return kind
	}
	if kind := strings.TrimSpace(jsonStringField(sse.Data, "type")); kind != "" {
		return kind
	}
	return sseUnknownKind
}

// recordWebsocketEvent mirrors SessionTelemetry's websocket event metric half
// (log_websocket_event -> codex.websocket.event): one counter and one duration
// sample per received websocket message, tagged by kind and success.
func recordWebsocketEvent(metrics MetricsSink, kind string, success bool, duration time.Duration) {
	if metrics == nil {
		return
	}
	if kind == "" {
		kind = websocketUnknownKind
	}
	tags := map[string]string{"kind": kind, "success": strconv.FormatBool(success)}
	metrics.Counter(websocketEventCountMetric, 1, tags)
	metrics.RecordDuration(websocketEventDurationMetric, duration, tags)
}

// recordWebsocketRequest mirrors SessionTelemetry::record_websocket_request:
// one counter and one duration sample per websocket request send, tagged by
// success (Rust's on_ws_request reports the send latency and its error).
func (r *ResponsesAgentRunner) recordWebsocketRequest(err error, duration time.Duration) {
	if r == nil || r.Metrics == nil {
		return
	}
	if duration < 0 {
		duration = 0
	}
	tags := map[string]string{"success": strconv.FormatBool(err == nil)}
	r.Metrics.Counter(websocketRequestCountMetric, 1, tags)
	r.Metrics.RecordDuration(websocketRequestDurationMetric, duration, tags)
}

// recordResponsesTimingMetrics mirrors
// SessionTelemetry::record_responses_websocket_timing_metrics: the six
// responses_api_* durations carried by a responsesapi.websocket_timing message.
func recordResponsesTimingMetrics(metrics MetricsSink, data []byte) {
	if metrics == nil {
		return
	}
	timing := responsesTimingMetricsFromEventData(data)
	if len(timing) == 0 {
		return
	}
	for _, field := range []struct {
		metric     string
		key        string
		fractional bool
	}{
		{responsesAPIOverheadDurationMetric, "responses_duration_excl_engine_and_client_tool_time_ms", false},
		{responsesAPIInferenceTimeDurationMetric, "engine_service_total_ms", false},
		{responsesAPIEngineIAPITTFTDurationMetric, "engine_iapi_ttft_total_ms", false},
		{responsesAPIEngineServiceTTFTDurationMetric, "engine_service_ttft_total_ms", false},
		{responsesAPIEngineIAPITBTDurationMetric, "engine_iapi_tbt_across_engine_calls_ms", true},
		{responsesAPIEngineServiceTBTDurationMetric, "engine_service_tbt_across_engine_calls_ms", true},
	} {
		milliseconds, ok := timingMetricMilliseconds(timing, field.key)
		if !ok {
			continue
		}
		if field.fractional {
			metrics.RecordDuration(field.metric, time.Duration(milliseconds*float64(time.Millisecond)), nil)
			continue
		}
		// Rust's duration_from_ms_value truncates to whole milliseconds.
		metrics.RecordDuration(field.metric, time.Duration(int64(milliseconds))*time.Millisecond, nil)
	}
}

// timingMetricMilliseconds reads one timing-metric value as milliseconds.
func timingMetricMilliseconds(timing map[string]any, key string) (float64, bool) {
	switch typed := timing[key].(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		value, err := typed.Float64()
		return value, err == nil
	}
	return 0, false
}
