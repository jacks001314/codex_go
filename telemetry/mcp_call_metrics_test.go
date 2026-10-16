package telemetry

import (
	"strings"
	"testing"
	"time"

	"codex_go/tool"
	"codex_go/turn"
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

// Rust #54094: an allowlisted `codex/telemetry` error code is the fallback
// after the structured content and connector authentication codes. The
// classification only reads the result, so the model-visible output is
// unchanged by construction (`MCPCallOutcomeForResult` takes no content).
func TestMCPCallOutcomeReadsTelemetryCodeLikeRust(t *testing.T) {
	meta := map[string]any{
		MCPResultTelemetryMetaKey: map[string]any{"error_code": "locator_timeout"},
	}
	got := MCPCallOutcomeForResult(true, true, nil, meta)
	want := MCPCallOutcome{Status: "error", ErrorType: MCPCallErrorTypeToolResult, ErrorCode: "locator_timeout"}
	if got != want {
		t.Fatalf("outcome = %#v, want %#v", got, want)
	}
}

// Rust #54094: the telemetry code sits behind the structured content and the
// connector authentication code, so it never wins over them.
func TestMCPCallOutcomePreservesErrorCodePrecedenceLikeRust(t *testing.T) {
	meta := map[string]any{
		MCPResultTelemetryMetaKey: map[string]any{"error_code": "locator_timeout"},
		"_codex_apps": map[string]any{
			"connector_auth_failure": map[string]any{
				"is_auth_failure": true,
				"error_code":      "UNAUTHORIZED",
			},
		},
	}
	cases := []struct {
		name              string
		structuredContent map[string]any
		wantCode          string
	}{
		{name: "structured content wins", structuredContent: map[string]any{"error_code": "RATE_LIMITED"}, wantCode: "RATE_LIMITED"},
		{name: "connector auth failure wins over telemetry", structuredContent: nil, wantCode: "UNAUTHORIZED"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := MCPCallOutcomeForResult(true, true, testCase.structuredContent, meta)
			if got.ErrorCode != testCase.wantCode {
				t.Fatalf("error code = %q, want %q", got.ErrorCode, testCase.wantCode)
			}
		})
	}
}

// Rust #54094: unrecognized metadata codes are ignored instead of being relayed,
// including values that carry a credential or a suffix.
func TestMCPCallOutcomeIgnoresInvalidTelemetryCodesLikeRust(t *testing.T) {
	metas := []map[string]any{
		{MCPResultTelemetryMetaKey: "invalid"},
		{MCPResultTelemetryMetaKey: map[string]any{"error_code": 42}},
		{MCPResultTelemetryMetaKey: map[string]any{"error_code": "Bearer credential-sentinel"}},
		{MCPResultTelemetryMetaKey: map[string]any{"error_code": "locator_timeout:secret-sentinel"}},
	}
	for _, meta := range metas {
		got := MCPCallOutcomeForResult(true, true, map[string]any{"error_code": ""}, meta)
		if got.ErrorCode != MCPCallErrorCodeUnknown {
			t.Fatalf("outcome = %#v for meta %#v, want the unknown code", got, meta)
		}
	}
}

// Rust #54094 (`caller_error_code_only_applies_to_request_failures`): a
// caller-classified request code replaces only an `mcp_request` failure's
// fallback code.
func TestMCPCallOutcomeRequestErrorCodeOnlyForRequestFailuresLikeRust(t *testing.T) {
	request := MCPCallOutcomeForResult(false, false, nil, nil).WithRequestErrorCode("request_timeout")
	wantRequest := MCPCallOutcome{Status: "error", ErrorType: MCPCallErrorTypeMCPRequest, ErrorCode: "request_timeout"}
	if request != wantRequest {
		t.Fatalf("request outcome = %#v, want %#v", request, wantRequest)
	}
	toolResult := MCPCallOutcomeForResult(true, true, map[string]any{"error_code": "locator_timeout"}, nil)
	if got, want := toolResult.WithRequestErrorCode("request_timeout"), toolResult; got != want {
		t.Fatalf("tool-result outcome = %#v, want it unchanged %#v", got, want)
	}
	if got := MCPCallOutcomeForResult(false, false, nil, nil).WithRequestErrorCode(""); got.ErrorCode != MCPCallErrorCodeUnknown {
		t.Fatalf("empty caller code = %#v, want the unknown fallback", got)
	}
}

// Rust #54046 (`McpCallMetricOutcome::approval_cancelled`): a cancelled MCP
// approval reports `error_type=approval`, `error_code=cancelled` and the
// reviewer that resolved it, defaulting to `unknown` for an absent or unknown
// source.
func TestMCPCallOutcomeApprovalCancelledLikeRust(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{source: "guardian", want: "guardian"},
		{source: "user", want: "user"},
		{source: "hook", want: "hook"},
		{source: "", want: "unknown"},
		{source: "bearer credential-sentinel", want: "unknown"},
	}
	for _, testCase := range cases {
		outcome := MCPCallOutcomeApprovalCancelled(testCase.source)
		if outcome.Status != "error" || outcome.ErrorType != "approval" || outcome.ErrorCode != "cancelled" {
			t.Fatalf("outcome = %#v, want error/approval/cancelled", outcome)
		}
		if outcome.ApprovalSource != testCase.want {
			t.Fatalf("approval source = %q, want %q", outcome.ApprovalSource, testCase.want)
		}
		sink := &recordingTurnMetricSink{}
		EmitMCPCallMetrics(sink, outcome, "example", "shell", "", "", 0)
		var found bool
		for _, counter := range sink.counters {
			if counter.name != MCPCallErrorCountMetric {
				continue
			}
			found = true
			if counter.tags["error_type"] != "approval" || counter.tags["error_code"] != "cancelled" {
				t.Fatalf("error tags = %#v", counter.tags)
			}
			if counter.tags["approval_source"] != testCase.want {
				t.Fatalf("approval_source tag = %q, want %q", counter.tags["approval_source"], testCase.want)
			}
		}
		if !found {
			t.Fatalf("no %s counter recorded", MCPCallErrorCountMetric)
		}
	}
	// A call that is not an approval cancellation never carries the tag.
	sink := &recordingTurnMetricSink{}
	EmitMCPCallMetrics(sink, MCPCallOutcome{Status: "error", ErrorType: MCPCallErrorTypeToolResult, ErrorCode: "rate_limited"}, "example", "shell", "", "", 0)
	for _, counter := range sink.counters {
		if counter.name != MCPCallErrorCountMetric {
			continue
		}
		if _, ok := counter.tags["approval_source"]; ok {
			t.Fatalf("approval_source leaked into a tool-result failure: %#v", counter.tags)
		}
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

// A completed execution is classified from the MCP executor's output shape, so
// every front-end (the app-server and the exec runtime) reports the same
// outcome; a call without the MCP server tag is not classified at all.
func TestMCPCallOutcomeForExecutionLikeRust(t *testing.T) {
	mcpExecution := &turn.ToolExecutionResult{
		Invocation:    &tool.Invocation{ToolName: tool.NamespacedName("mcp__example", "shell")},
		Output:        &tool.Output{Data: map[string]any{"mcpToolCall": true, "isError": true, "structuredContent": map[string]any{"error_code": "rate_limited"}, "server": "example", "tool": "shell"}},
		TelemetryTags: map[string]string{"mcp_server": "example", "mcp_server_origin": "stdio"},
		StartedAt:     time.Unix(1700000000, 0),
		FinishedAt:    time.Unix(1700000000, 0).Add(25 * time.Millisecond),
	}
	outcome, ok := MCPCallOutcomeForExecution(mcpExecution)
	if !ok || outcome != (MCPCallOutcome{Status: "error", ErrorType: MCPCallErrorTypeToolResult, ErrorCode: "rate_limited"}) {
		t.Fatalf("outcome = %#v ok=%v", outcome, ok)
	}
	if server, toolName := MCPCallNames(mcpExecution); server != "example" || toolName != "shell" {
		t.Fatalf("names = %q/%q", server, toolName)
	}
	sink := &recordingTurnMetricSink{}
	if !EmitMCPCallMetricsForExecution(sink, mcpExecution, "connector-1", "Calendar") {
		t.Fatal("MCP execution was not classified")
	}
	if len(sink.counters) != 2 || len(sink.durations) != 1 {
		t.Fatalf("counters = %#v durations = %#v", sink.counters, sink.durations)
	}
	if sink.counters[0].tags["server"] != "example" || sink.counters[0].tags["connector_id"] != "connector-1" ||
		sink.counters[1].tags["error_code"] != "rate_limited" {
		t.Fatalf("counters = %#v", sink.counters)
	}

	// A non-MCP call reports nothing.
	plain := &turn.ToolExecutionResult{Invocation: &tool.Invocation{ToolName: tool.PlainName("shell")}, Output: &tool.Output{Success: true}}
	if _, ok := MCPCallOutcomeForExecution(plain); ok {
		t.Fatal("a non-MCP execution was classified")
	}
	if EmitMCPCallMetricsForExecution(sink, plain, "", "") {
		t.Fatal("a non-MCP execution emitted MCP metrics")
	}
	if len(sink.counters) != 2 {
		t.Fatalf("counters = %#v", sink.counters)
	}

	// A failed MCP call whose executor never received a result is Rust's
	// mcp_request failure, and its tag names fall back to the invocation.
	failed := &turn.ToolExecutionResult{
		Invocation:    &tool.Invocation{ToolName: tool.NamespacedName("mcp__example", "shell")},
		TelemetryTags: map[string]string{"mcp_server": "example"},
	}
	requestOutcome, ok := MCPCallOutcomeForExecution(failed)
	if !ok || requestOutcome.ErrorType != MCPCallErrorTypeMCPRequest || requestOutcome.ErrorCode != MCPCallErrorCodeUnknown {
		t.Fatalf("outcome = %#v ok=%v", requestOutcome, ok)
	}
	if _, toolName := MCPCallNames(failed); toolName != "mcp__example.shell" {
		t.Fatalf("fallback tool name = %q", toolName)
	}
}
