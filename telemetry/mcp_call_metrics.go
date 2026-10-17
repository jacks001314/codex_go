package telemetry

import (
	"strconv"
	"strings"
	"time"

	"codex_go/tool"
	"codex_go/turn"
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
	// ApprovalSource names the reviewer that resolved a cancelled approval
	// (Rust #54046 `approval_source`): `guardian`, `user`, `hook` or `unknown`.
	// It identifies the reviewer, not who caused the cancellation.
	ApprovalSource string
}

// MCPCallOutcomeApprovalCancelled builds Rust's
// `McpCallMetricOutcome::approval_cancelled` (#54046): a cancelled MCP approval
// reported as `error_type=approval`, `error_code=cancelled` plus the reviewer
// that resolved it.
func MCPCallOutcomeApprovalCancelled(approvalSource string) MCPCallOutcome {
	source := strings.TrimSpace(strings.ToLower(approvalSource))
	switch source {
	case "guardian", "user", "hook":
	default:
		source = "unknown"
	}
	return MCPCallOutcome{
		Status:         "error",
		ErrorType:      "approval",
		ErrorCode:      "cancelled",
		ApprovalSource: source,
	}
}

// WithRequestErrorCode carries a caller-classified request failure code into an
// outcome, mirroring Rust `McpCallMetricOutcome::with_request_error_code`
// (#54094): the code only replaces the fallback `unknown` code of an
// `mcp_request` failure, so a tool-result code is never overwritten.
func (outcome MCPCallOutcome) WithRequestErrorCode(code string) MCPCallOutcome {
	if outcome.ErrorType == MCPCallErrorTypeMCPRequest && code != "" {
		outcome.ErrorCode = code
	}
	return outcome
}

// allowlistedCodexTelemetryErrorCode reports whether a `codex/telemetry`
// `error_code` is one of the bounded codes Rust's
// `allowlisted_codex_telemetry_error_code` accepts (#54094). Anything else - an
// unknown word, a non-string, a value carrying a credential or a suffix - is
// rejected instead of being relayed into metrics and spans.
func allowlistedCodexTelemetryErrorCode(code string) (string, bool) {
	switch code {
	case "kernel_start_failed",
		"kernel_exited",
		"kernel_io_error",
		"execution_timeout",
		"execution_cancelled",
		"execution_error",
		"execution_rejected",
		"execution_reset",
		"import_error",
		"syntax_error",
		"reference_error",
		"type_error",
		"uncaught_exception",
		"unhandled_rejection",
		"locator_timeout",
		"guardian_denied":
		return code, true
	}
	return code, false
}

// codexTelemetryErrorCode reads the allowlisted `error_code` a result reports
// through `_meta["codex/telemetry"]` (Rust's fallback after the structured
// content and connector authentication codes).
func codexTelemetryErrorCode(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	telemetryMeta, _ := meta[MCPResultTelemetryMetaKey].(map[string]any)
	if telemetryMeta == nil {
		return ""
	}
	code, _ := telemetryMeta["error_code"].(string)
	if code == "" {
		return ""
	}
	if allowlisted, ok := allowlistedCodexTelemetryErrorCode(code); ok {
		return allowlisted
	}
	return ""
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
		// Rust #54094: an allowlisted `codex/telemetry` code is the last
		// fallback before the `unknown` code.
		errorCode = codexTelemetryErrorCode(meta)
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
	if source := strings.TrimSpace(outcome.ApprovalSource); source != "" {
		// Rust #54046: the source identifies the reviewer, not who cancelled.
		errorTags["approval_source"] = SanitizeMetricTagValue(source)
	}
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

// MCPCallOutcomeForExecution classifies one completed tool execution as an MCP
// call, or reports false when the call is not an MCP call at all (no MCP server
// tag). The result shape is the MCP executor's `tool.Output.Data`
// (`mcpToolCall`, `isError`, `structuredContent`, `_meta`); a call whose executor
// failed before the server answered carries none of it and is Rust's
// `mcp_request` failure.
func MCPCallOutcomeForExecution(execution *turn.ToolExecutionResult) (MCPCallOutcome, bool) {
	if execution == nil || execution.Invocation == nil {
		return MCPCallOutcome{}, false
	}
	if strings.TrimSpace(execution.TelemetryTags["mcp_server"]) == "" {
		return MCPCallOutcome{}, false
	}
	data := MCPExecutionOutputData(execution)
	hasResult := false
	isError := false
	var structuredContent map[string]any
	if data != nil {
		hasResult, _ = data["mcpToolCall"].(bool)
		isError, _ = data["isError"].(bool)
		structuredContent, _ = data["structuredContent"].(map[string]any)
	}
	outcome := MCPCallOutcomeForResult(hasResult, isError, structuredContent, MCPExecutionResultMeta(execution))
	// Rust #54094: a request failure classified by the transport layer keeps
	// its bounded code (`request_timeout`, `transport_closed`, ...) instead of
	// the `unknown` fallback.
	outcome = outcome.WithRequestErrorCode(execution.TelemetryTags[tool.RequestErrorCodeTelemetryTag])
	return outcome, true
}

// MCPExecutionOutputData reports a tool execution's output data, or nil when the
// call produced no output.
func MCPExecutionOutputData(execution *turn.ToolExecutionResult) map[string]any {
	if execution == nil || execution.Output == nil {
		return nil
	}
	return execution.Output.Data
}

// MCPExecutionResultMeta reads an MCP result's `_meta` object from a tool
// execution's output data.
func MCPExecutionResultMeta(execution *turn.ToolExecutionResult) map[string]any {
	data := MCPExecutionOutputData(execution)
	if data == nil {
		return nil
	}
	meta, _ := data["_meta"].(map[string]any)
	return meta
}

// MCPCallNames reports the server and tool names an MCP call's metrics are tagged
// with: the server the call ran against and the MCP tool's own name (Rust tags
// the remote tool name, not the namespaced one).
func MCPCallNames(execution *turn.ToolExecutionResult) (string, string) {
	if execution == nil {
		return "", ""
	}
	server := strings.TrimSpace(execution.TelemetryTags["mcp_server"])
	toolName := ""
	if data := MCPExecutionOutputData(execution); data != nil {
		toolName, _ = data["tool"].(string)
	}
	toolName = strings.TrimSpace(toolName)
	if toolName == "" && execution.Invocation != nil {
		toolName = execution.Invocation.ToolName.Key()
	}
	return server, toolName
}

// EmitMCPCallMetricsForExecution records Rust's MCP call metric triple for one
// completed execution, tagging the connector the call targets. It reports false
// when the execution is not an MCP call.
func EmitMCPCallMetricsForExecution(sink TurnMetricSink, execution *turn.ToolExecutionResult, connectorID string, connectorName string) bool {
	outcome, ok := MCPCallOutcomeForExecution(execution)
	if !ok {
		return false
	}
	server, toolName := MCPCallNames(execution)
	var duration time.Duration
	if execution != nil {
		duration = execution.FinishedAt.Sub(execution.StartedAt)
	}
	EmitMCPCallMetrics(sink, outcome, server, toolName, connectorID, connectorName, duration)
	return true
}
