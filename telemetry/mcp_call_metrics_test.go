package telemetry

import (
	"strings"
	"testing"
	"time"
)

// MCPCallOutcomeForResult mirrors Rust's mcp_call_metric_outcome matrix.
func TestMCPCallOutcomeForResultLikeRust(t *testing.T) {
	cases := []struct {
		name              string
		hasResult         bool
		isError           bool
		structuredContent map[string]any
		meta              map[string]any
		want              MCPCallOutcome
	}{
		{
			name:      "request failure",
			hasResult: false,
			want:      MCPCallOutcome{Status: "error", ErrorType: MCPCallErrorTypeMCPRequest, ErrorCode: MCPCallErrorCodeUnknown},
		},
		{
			name:      "successful result",
			hasResult: true,
			want:      MCPCallOutcome{Status: "ok"},
		},
		{
			name:      "tool result error code",
			hasResult: true,
			isError:   true,
			structuredContent: map[string]any{
				"error_code": "rate_limited",
			},
			want: MCPCallOutcome{Status: "error", ErrorType: MCPCallErrorTypeToolResult, ErrorCode: "rate_limited"},
		},
		{
			name:      "connector auth failure fallback",
			hasResult: true,
			isError:   true,
			meta: map[string]any{
				"_codex_apps": map[string]any{
					"connector_auth_failure": map[string]any{
						"is_auth_failure": true,
						"error_code":      "invalid_grant",
					},
				},
			},
			want: MCPCallOutcome{Status: "error", ErrorType: MCPCallErrorTypeToolResult, ErrorCode: "invalid_grant"},
		},
		{
			name:      "auth failure flag off is ignored",
			hasResult: true,
			isError:   true,
			meta: map[string]any{
				"_codex_apps": map[string]any{
					"connector_auth_failure": map[string]any{"is_auth_failure": false, "error_code": "invalid_grant"},
				},
			},
			want: MCPCallOutcome{Status: "error", ErrorType: MCPCallErrorTypeToolResult, ErrorCode: MCPCallErrorCodeUnknown},
		},
		{
			name:      "tool result error without code",
			hasResult: true,
			isError:   true,
			want:      MCPCallOutcome{Status: "error", ErrorType: MCPCallErrorTypeToolResult, ErrorCode: MCPCallErrorCodeUnknown},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := MCPCallOutcomeForResult(testCase.hasResult, testCase.isError, testCase.structuredContent, testCase.meta)
			if got != testCase.want {
				t.Fatalf("outcome = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// A reported error code longer than the cap is truncated like Rust's
// truncate_str_to_char_boundary.
func TestMCPCallOutcomeTruncatesErrorCodeLikeRust(t *testing.T) {
	code := strings.Repeat("é", MCPCallErrorCodeMaxChars+5)
	outcome := MCPCallOutcomeForResult(true, true, map[string]any{"error_code": code}, nil)
	if len([]rune(outcome.ErrorCode)) != MCPCallErrorCodeMaxChars {
		t.Fatalf("error code runes = %d, want %d", len([]rune(outcome.ErrorCode)), MCPCallErrorCodeMaxChars)
	}
	if outcome.ErrorCode != strings.Repeat("é", MCPCallErrorCodeMaxChars) {
		t.Fatalf("error code = %q", outcome.ErrorCode)
	}
}

// The metric triple follows Rust's emit_mcp_call_metrics: the tagged count and
// duration for every call, and the error counter with the outcome's extra tags.
func TestEmitMCPCallMetricsLikeRust(t *testing.T) {
	sink := &recordingTurnMetricSink{}
	outcome := MCPCallOutcome{Status: "error", ErrorType: MCPCallErrorTypeToolResult, ErrorCode: "rate_limited"}
	EmitMCPCallMetrics(sink, outcome, "example", "shell", "calendar", "Calendar", 25*time.Millisecond)
	if len(sink.counters) != 2 || len(sink.durations) != 1 {
		t.Fatalf("counters = %#v durations = %#v", sink.counters, sink.durations)
	}
	wantTags := map[string]string{
		"status":         "error",
		"server":         "example",
		"tool":           "shell",
		"connector_id":   "calendar",
		"connector_name": "Calendar",
	}
	call := sink.counters[0]
	if call.name != MCPCallCountMetric || call.value != 1 {
		t.Fatalf("call counter = %#v", call)
	}
	for key, want := range wantTags {
		if got := call.tags[key]; got != want {
			t.Fatalf("call tag %s = %q, want %q", key, got, want)
		}
	}
	if duration := sink.durations[0]; duration.name != MCPCallDurationMetric || duration.duration != 25*time.Millisecond {
		t.Fatalf("duration = %#v", duration)
	}
	failure := sink.counters[1]
	if failure.name != MCPCallErrorCountMetric || failure.tags["error_type"] != MCPCallErrorTypeToolResult ||
		failure.tags["error_code"] != "rate_limited" {
		t.Fatalf("error counter = %#v", failure)
	}
}

// A successful call reports no error counter, and a call without connector
// metadata omits the connector tags.
func TestEmitMCPCallMetricsOmitsOptionalTagsLikeRust(t *testing.T) {
	sink := &recordingTurnMetricSink{}
	EmitMCPCallMetrics(sink, MCPCallOutcome{Status: "ok"}, "example", "shell", "", "", time.Second)
	if len(sink.counters) != 1 {
		t.Fatalf("counters = %#v", sink.counters)
	}
	for _, key := range []string{"connector_id", "connector_name", "error_type", "error_code"} {
		if _, ok := sink.counters[0].tags[key]; ok {
			t.Fatalf("counter tags = %#v", sink.counters[0].tags)
		}
	}
}

// The result's `_meta["codex/telemetry"]["span"]` telemetry becomes the call
// span's target id and server-user-flow attributes; the outcome adds the error
// attributes (Rust's record_mcp_result_span_telemetry).
func TestMCPCallSpanAttributesLikeRust(t *testing.T) {
	meta := map[string]any{
		"codex/telemetry": map[string]any{
			"span": map[string]any{
				"target_id":                    "target-1",
				"did_trigger_server_user_flow": true,
			},
		},
	}
	attributes := MCPCallSpanAttributes(MCPCallOutcome{
		Status:    "error",
		ErrorType: MCPCallErrorTypeToolResult,
		ErrorCode: "rate_limited",
	}, meta)
	want := map[string]string{
		MCPCallErrorTypeSpanAttr:        MCPCallErrorTypeToolResult,
		MCPCallErrorCodeSpanAttr:        "rate_limited",
		MCPResultTargetIDSpanAttr:       "target-1",
		MCPResultServerUserFlowSpanAttr: "true",
	}
	for key, value := range want {
		if got := attributes[key]; got != value {
			t.Fatalf("attribute %s = %q, want %q", key, got, value)
		}
	}

	// A result without span telemetry and a successful outcome records nothing.
	if attributes := MCPCallSpanAttributes(MCPCallOutcome{Status: "ok"}, nil); len(attributes) != 0 {
		t.Fatalf("attributes = %#v", attributes)
	}
	// An empty target id is not reported, and the boolean is only present when the
	// server sent one.
	targetID, userFlow, ok := MCPResultSpanTelemetry(map[string]any{
		"codex/telemetry": map[string]any{"span": map[string]any{"target_id": "  "}},
	})
	if !ok || targetID != "" || userFlow != nil {
		t.Fatalf("span telemetry = %q %v %v", targetID, userFlow, ok)
	}
}
