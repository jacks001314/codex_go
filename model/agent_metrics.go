package model

import (
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
	apiCallCountMetric     = "codex.api_request"
	apiCallDurationMetric  = "codex.api_request.duration_ms"
	sseEventCountMetric    = "codex.sse_event"
	sseEventDurationMetric = "codex.sse_event.duration_ms"
)

// sseUnknownKind mirrors SSE_UNKNOWN_KIND.
const sseUnknownKind = "unknown"

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
