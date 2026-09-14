package appserver

import (
	"strings"

	"codex_go/telemetry"
	"codex_go/tool"
)

// Rust parity: codex-core's `mcp_tool_call_span` (core/src/mcp_tool_call.rs) and
// its result recording (`record_mcp_result_span_telemetry`). The span brackets
// one MCP tool call, so the trace-safe records the call emits attach to it.

// mcpToolCallSpanKey identifies one in-flight MCP tool call span.
type mcpToolCallSpanKey struct {
	threadID string
	turnID   string
	callID   string
}

// startMCPToolCallSpan starts the `mcp.tools.call` span for an MCP invocation, or
// returns nil when the call is not an MCP call or tracing is off.
func (r *RuntimeRouter) startMCPToolCallSpan(invocation *tool.Invocation, threadID string, turnID string) *telemetry.Span {
	if r == nil || invocation == nil || r.services.ToolRouter == nil {
		return nil
	}
	tracer := r.requestTracer()
	if tracer == nil {
		return nil
	}
	tags := r.services.ToolRouter.TelemetryTags(invocation)
	serverName := strings.TrimSpace(tags["mcp_server"])
	if serverName == "" {
		// Only MCP calls carry the server tag (Rust's ToolRuntime::mcp_server_name).
		return nil
	}
	origin := strings.TrimSpace(tags["mcp_server_origin"])
	span := tracer.StartSpanWithKind("mcp.tools.call", telemetry.SpanKindClient, map[string]string{
		"rpc.system":        "jsonrpc",
		"rpc.method":        "tools/call",
		"mcp.server.name":   serverName,
		"mcp.server.origin": origin,
		"mcp.transport":     mcpTransportForOrigin(origin),
		// Rust records the connector fields as empty strings when the call has no
		// connector, so the attributes stay present.
		"mcp.connector.id":   "",
		"mcp.connector.name": "",
		"tool.name":          strings.TrimSpace(invocation.ToolName.Name),
		"tool.call_id":       strings.TrimSpace(invocation.CallID),
		"conversation.id":    strings.TrimSpace(threadID),
		"session.id":         strings.TrimSpace(threadID),
		"turn.id":            strings.TrimSpace(turnID),
	})
	if span == nil {
		return nil
	}
	key := mcpToolCallSpanKey{threadID: strings.TrimSpace(threadID), turnID: strings.TrimSpace(turnID), callID: strings.TrimSpace(invocation.CallID)}
	r.mcpCallSpansMu.Lock()
	if r.mcpCallSpans == nil {
		r.mcpCallSpans = map[mcpToolCallSpanKey]*telemetry.Span{}
	}
	r.mcpCallSpans[key] = span
	r.mcpCallSpansMu.Unlock()
	return span
}

// takeMCPToolCallSpan removes and returns the span of a completed call.
func (r *RuntimeRouter) takeMCPToolCallSpan(threadID string, turnID string, callID string) *telemetry.Span {
	if r == nil {
		return nil
	}
	key := mcpToolCallSpanKey{threadID: strings.TrimSpace(threadID), turnID: strings.TrimSpace(turnID), callID: strings.TrimSpace(callID)}
	r.mcpCallSpansMu.Lock()
	defer r.mcpCallSpansMu.Unlock()
	span := r.mcpCallSpans[key]
	delete(r.mcpCallSpans, key)
	return span
}

// endMCPToolCallSpansForTurn closes the spans a turn left open (a call that never
// reported completion), so their telemetry is still exported.
func (r *RuntimeRouter) endMCPToolCallSpansForTurn(threadID string, turnID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	r.mcpCallSpansMu.Lock()
	spans := make([]*telemetry.Span, 0, len(r.mcpCallSpans))
	for key, span := range r.mcpCallSpans {
		if key.threadID == threadID && key.turnID == turnID {
			spans = append(spans, span)
			delete(r.mcpCallSpans, key)
		}
	}
	r.mcpCallSpansMu.Unlock()
	for _, span := range spans {
		span.End()
	}
}

// mcpTransportForOrigin mirrors mcp_tool_call_span's transport mapping.
func mcpTransportForOrigin(origin string) string {
	switch strings.TrimSpace(origin) {
	case "stdio":
		return "stdio"
	case "in_process":
		return "in_process"
	case "":
		return ""
	default:
		return "streamable_http"
	}
}
