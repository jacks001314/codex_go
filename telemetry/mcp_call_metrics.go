package telemetry

import (
	"strconv"
	"strings"
	"time"
)

// Rust parity: codex-core's `mcp_tool_call/telemetry.rs` (the codex.mcp.call
// metric pair, the error metric, and the outcome classification) plus
// `mcp_tool_call.rs`'s `record_mcp_result_span_telemetry`, which records the
// call's outcome and result telemetry on the call's span.

const (
	// MCPCallCountMetric counts every MCP tool call (Rust MCP_CALL_COUNT_METRIC).
	MCPCallCountMetric = "codex.mcp.call"
	// MCPCallDurationMetric times every MCP tool call.
	MCPCallDurationMetric = "codex.mcp.call.duration_ms"
	// MCPCallErrorCountMetric counts failed MCP tool calls.
	MCPCallErrorCountMetric = "codex.mcp.call.error"

	// MCPCallErrorTypeMCPRequest means no CallToolResult was received: request
	// setup, transport, timeout, protocol, or JSON-RPC failures.
	MCPCallErrorTypeMCPRequest = "mcp_request"
	// MCPCallErrorTypeToolResult means the server returned isError=true.
	MCPCallErrorTypeToolResult = "tool_result"
	// MCPCallErrorCodeUnknown is the fallback error code.
	MCPCallErrorCodeUnknown = "unknown"
	// MCPCallErrorCodeMaxChars caps a reported error code (Rust truncates to
	// char boundary).
	MCPCallErrorCodeMaxChars = 256
)

// MCP call span attribute keys (Rust's span attribute constants).
const (
	MCPCallErrorTypeSpanAttr        = "error.type"
	MCPCallErrorCodeSpanAttr        = "codex.mcp.error.code"
	MCPResultTargetIDSpanAttr       = "codex.mcp.target.id"
	MCPResultServerUserFlowSpanAttr = "codex.mcp.server_user_flow.triggered"
	// MCPResultTargetIDMaxChars caps the reported target id.
	MCPResultTargetIDMaxChars = 256
)

// MCP result telemetry keys: a result's `_meta["codex/telemetry"]["span"]`
// carries the target id and whether the call triggered a server user flow
// (Rust MCP_RESULT_TELEMETRY_*).
const (
	MCPResultTelemetryMetaKey     = "codex/telemetry"
	MCPResultTelemetrySpanKey     = "span"
	MCPResultTelemetryTargetIDKey = "target_id"
	MCPResultTelemetryUserFlowKey = "did_trigger_server_user_flow"
	// MCPCallOutcomeMetricTagStatus names the status tag Rust reports.
	MCPCallOutcomeMetricTagStatus = "status"
)

// MCPCallOutcome is Rust's McpCallMetricOutcome: the status tag plus the
// optional error classification of one MCP tool call.
type MCPCallOutcome struct {
	Status    string
	ErrorType string
	ErrorCode string
}

// MCPCallOutcomeForResult classifies one MCP tool call the way Rust's
// `mcp_call_metric_outcome` does. hasResult is false when no CallToolResult was
// received (the executor failed before the tool answered).
func MCPCallOutcomeForResult(hasResult bool, isError bool, structuredContent map[string]any, meta map[string]any) MCPCallOutcome {
	if !hasResult {
		return MCPCallOutcome{
			Status:    "error",
			ErrorType: MCPCallErrorTypeMCPRequest,
			ErrorCode: MCPCallErrorCodeUnknown,
		}
	}
	if !isError {
		return MCPCallOutcome{Status: "ok"}
	}
	errorCode := ""
	if structuredContent != nil {
		errorCode = stringFromAnyMap(structuredContent, "error_code")
	}
	if errorCode == "" {
		errorCode = mcpConnectorAuthFailureErrorCode(meta)
	}
	if errorCode == "" {
		errorCode = MCPCallErrorCodeUnknown
	}
	return MCPCallOutcome{
		Status:    "error",
		ErrorType: MCPCallErrorTypeToolResult,
		ErrorCode: truncateChars(errorCode, MCPCallErrorCodeMaxChars),
	}
}

// mcpConnectorAuthFailureErrorCode reads the connector auth failure's error code
// from a result's `_meta._codex_apps` (Rust's MCP_TOOL_CODEX_APPS_META_KEY arm).
func mcpConnectorAuthFailureErrorCode(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	codexApps, _ := meta["_codex_apps"].(map[string]any)
	if codexApps == nil {
		return ""
	}
	authFailure, _ := codexApps["connector_auth_failure"].(map[string]any)
	if authFailure == nil {
		return ""
	}
	flag, _ := authFailure["is_auth_failure"].(bool)
	if !flag {
		return ""
	}
	return stringFromAnyMap(authFailure, "error_code")
}

// MCPCallMetricTags builds Rust's `mcp_call_metric_tags`: the status, the server
// and tool, and the connector identity when the call has one.
func MCPCallMetricTags(outcome MCPCallOutcome, server string, toolName string, connectorID string, connectorName string) map[string]string {
	tags := map[string]string{
		MCPCallOutcomeMetricTagStatus: SanitizeMetricTagValue(outcome.Status),
		"server":                      SanitizeMetricTagValue(server),
		"tool":                        SanitizeMetricTagValue(toolName),
	}
	if connectorID = strings.TrimSpace(connectorID); connectorID != "" {
		tags["connector_id"] = SanitizeMetricTagValue(connectorID)
	}
	if connectorName = strings.TrimSpace(connectorName); connectorName != "" {
		tags["connector_name"] = SanitizeMetricTagValue(connectorName)
	}
	return tags
}

// EmitMCPCallMetrics records Rust's MCP call metric triple: the count and
// duration for every call, and the error count with Rust's extra error tags.
func EmitMCPCallMetrics(sink TurnMetricSink, outcome MCPCallOutcome, server string, toolName string, connectorID string, connectorName string, duration time.Duration) {
	if sink == nil {
		return
	}
	tags := MCPCallMetricTags(outcome, server, toolName, connectorID, connectorName)
	sink.Counter(MCPCallCountMetric, 1, tags)
	if duration < 0 {
		duration = 0
	}
	sink.RecordDuration(MCPCallDurationMetric, duration, tags)
	if outcome.ErrorType == "" || outcome.ErrorCode == "" {
		return
	}
	errorTags := make(map[string]string, len(tags)+2)
	for key, value := range tags {
		errorTags[key] = value
	}
	errorTags["error_type"] = SanitizeMetricTagValue(outcome.ErrorType)
	errorTags["error_code"] = outcome.ErrorCode
	sink.Counter(MCPCallErrorCountMetric, 1, errorTags)
}

// MCPCallSpanAttributes reports the span attributes Rust records for one call:
// the outcome's error classification and the result's own span telemetry.
func MCPCallSpanAttributes(outcome MCPCallOutcome, meta map[string]any) map[string]string {
	attributes := map[string]string{}
	if outcome.ErrorType != "" && outcome.ErrorCode != "" {
		attributes[MCPCallErrorTypeSpanAttr] = outcome.ErrorType
		attributes[MCPCallErrorCodeSpanAttr] = outcome.ErrorCode
	}
	targetID, userFlow, ok := MCPResultSpanTelemetry(meta)
	if !ok {
		return attributes
	}
	if targetID != "" {
		attributes[MCPResultTargetIDSpanAttr] = truncateChars(targetID, MCPResultTargetIDMaxChars)
	}
	if userFlow != nil {
		attributes[MCPResultServerUserFlowSpanAttr] = strconv.FormatBool(*userFlow)
	}
	return attributes
}

// MCPResultSpanTelemetry extracts the result's `_meta["codex/telemetry"]["span"]`
// telemetry: the target id (absent when empty) and the server-user-flow flag
// (absent when the server did not report one). ok is false when the result
// carries no span telemetry at all.
func MCPResultSpanTelemetry(meta map[string]any) (targetID string, userFlow *bool, ok bool) {
	if meta == nil {
		return "", nil, false
	}
	telemetryMeta, _ := meta[MCPResultTelemetryMetaKey].(map[string]any)
	if telemetryMeta == nil {
		return "", nil, false
	}
	spanMeta, _ := telemetryMeta[MCPResultTelemetrySpanKey].(map[string]any)
	if spanMeta == nil {
		return "", nil, false
	}
	if value, found := spanMeta[MCPResultTelemetryTargetIDKey].(string); found && strings.TrimSpace(value) != "" {
		targetID = value
	}
	if value, found := spanMeta[MCPResultTelemetryUserFlowKey].(bool); found {
		flow := value
		userFlow = &flow
	}
	return targetID, userFlow, true
}

// truncateChars keeps at most maxChars runes, mirroring Rust's
// truncate_str_to_char_boundary.
func truncateChars(value string, maxChars int) string {
	if maxChars <= 0 {
		return value
	}
	count := 0
	for index := range value {
		if count == maxChars {
			return value[:index]
		}
		count++
	}
	return value
}

// stringFromAnyMap reads a non-empty string entry.
func stringFromAnyMap(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	text, _ := values[key].(string)
	return strings.TrimSpace(text)
}
