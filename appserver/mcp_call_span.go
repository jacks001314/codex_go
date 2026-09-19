package appserver

import (
	"context"
	"log/slog"
	"strings"

	"codex_go/telemetry"
	"codex_go/tool"
	"codex_go/turn"
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
// returns nil when the call is not an MCP call or tracing is off. The span
// parents to the span ctx carries - the step's sampling request, which Rust keeps
// open while it drains the step's in-flight tool futures.
func (r *RuntimeRouter) startMCPToolCallSpan(ctx context.Context, invocation *tool.Invocation, threadID string, turnID string) *telemetry.Span {
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
	connectorID, connectorName := r.mcpConnectorInfo(invocation)
	attributes := map[string]string{
		"rpc.system":        "jsonrpc",
		"rpc.method":        "tools/call",
		"mcp.server.name":   serverName,
		"mcp.server.origin": origin,
		"mcp.transport":     mcpTransportForOrigin(origin),
		// Rust records the connector fields as empty strings when the call has no
		// connector, so the attributes stay present.
		"mcp.connector.id":   connectorID,
		"mcp.connector.name": connectorName,
		"tool.name":          strings.TrimSpace(invocation.ToolName.Name),
		"tool.call_id":       strings.TrimSpace(invocation.CallID),
		"conversation.id":    strings.TrimSpace(threadID),
		"session.id":         strings.TrimSpace(threadID),
		"turn.id":            strings.TrimSpace(turnID),
	}
	var span *telemetry.Span
	if parent := telemetry.SpanFromContext(ctx); parent != nil {
		span = tracer.StartSpanWithParentAndKind(parent, "mcp.tools.call", telemetry.SpanKindClient, attributes)
	} else {
		span = tracer.StartSpanWithKind("mcp.tools.call", telemetry.SpanKindClient, attributes)
	}
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

// Rust parity: codex-core's session/mod.rs spawns every session inside a
// `thread_spawn` span parented to the spawning request's W3C carrier, and runs
// the session's submission loop inside a `session_loop` span (thread_id) that
// stays open for the thread's whole life, so every turn's spans nest under it.

// ThreadSpawnSpanName is the span Rust opens around a session spawn.
const ThreadSpawnSpanName = "thread_spawn"

// SessionLoopSpanName is the span Rust opens around a session's submission loop.
const SessionLoopSpanName = "session_loop"

// runtimeUnifiedExecSpanSink opens the unified-exec lifecycle spans on the
// session's tracer (#45505). The tracer is resolved per span, so an OTEL
// provider installed or reloaded after the manager was built still receives
// them; telemetry.Span already carries the tool layer's span surface.
func (r *RuntimeRouter) runtimeUnifiedExecSpanSink() tool.UnifiedExecSpanSink {
	return func(name string, attributes map[string]string) tool.UnifiedExecSpan {
		if r == nil {
			return nil
		}
		tracer := r.requestTracer()
		if tracer == nil {
			return nil
		}
		return tool.AdaptUnifiedExecSpan(tracer.StartSpan(name, attributes))
	}
}

// threadSpawnTarget reports whether a lifecycle request spawns a session. Rust
// reaches ThreadManager::spawn_thread (and so session/mod.rs::spawn) for a new or
// forked thread, and for a resume that finds no live session.
func (r *RuntimeRouter) threadSpawnTarget(request *Request) bool {
	if r == nil || request == nil {
		return false
	}
	switch request.Method {
	case MethodThreadStart, MethodThreadFork:
		return true
	default:
		return false
	}
}

// startThreadSpawnSpan opens the `thread_spawn` span for a thread-creating
// request, continuing the request's W3C carrier like Rust's
// `set_parent_from_w3c_trace_context`.
func (r *RuntimeRouter) startThreadSpawnSpan(request *Request) *telemetry.Span {
	if r == nil {
		return nil
	}
	tracer := r.requestTracer()
	if tracer == nil {
		return nil
	}
	span := tracer.StartSpan(ThreadSpawnSpanName, nil)
	if span == nil {
		return nil
	}
	if request != nil && request.Trace != nil && strings.TrimSpace(request.Trace.Traceparent) != "" {
		if traceContext, ok := telemetry.ParseTraceContext(request.Trace.Traceparent, request.Trace.Tracestate); ok {
			if !span.SetParentContext(traceContext) {
				slog.Warn("ignoring invalid thread spawn trace carrier")
			}
		}
	} else if traceContext, ok := telemetry.TraceContextFromEnv(); ok {
		span.SetParentContext(traceContext)
	}
	return span
}

// startThreadSessionSpan opens and records the thread's `session_loop` span as a
// child of the spawn span. An already live session keeps its span (a repeated
// resume does not start a second loop).
func (r *RuntimeRouter) startThreadSessionSpan(threadID string, parent *telemetry.Span) *telemetry.Span {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	r.threadSessionSpansMu.Lock()
	if existing := r.threadSessionSpans[threadID]; existing != nil {
		r.threadSessionSpansMu.Unlock()
		return existing
	}
	r.threadSessionSpansMu.Unlock()
	tracer := r.requestTracer()
	if tracer == nil {
		return nil
	}
	span := tracer.StartSpanWithParent(parent, SessionLoopSpanName, map[string]string{"thread_id": threadID})
	if span == nil {
		return nil
	}
	r.threadSessionSpansMu.Lock()
	if r.threadSessionSpans == nil {
		r.threadSessionSpans = map[string]*telemetry.Span{}
	}
	r.threadSessionSpans[threadID] = span
	r.threadSessionSpansMu.Unlock()
	return span
}

// threadSessionSpan returns the live session span of a thread, or nil.
func (r *RuntimeRouter) threadSessionSpan(threadID string) *telemetry.Span {
	if r == nil {
		return nil
	}
	r.threadSessionSpansMu.Lock()
	defer r.threadSessionSpansMu.Unlock()
	return r.threadSessionSpans[strings.TrimSpace(threadID)]
}

// endThreadSessionSpan closes a thread's session loop span (Rust ends it when the
// session's submission loop terminates).
func (r *RuntimeRouter) endThreadSessionSpan(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	r.threadSessionSpansMu.Lock()
	span := r.threadSessionSpans[threadID]
	delete(r.threadSessionSpans, threadID)
	r.threadSessionSpansMu.Unlock()
	if span != nil {
		span.End()
	}
}

// endAllThreadSessionSpans closes every live session span when the router shuts
// down.
func (r *RuntimeRouter) endAllThreadSessionSpans() {
	if r == nil {
		return
	}
	r.threadSessionSpansMu.Lock()
	spans := make([]*telemetry.Span, 0, len(r.threadSessionSpans))
	for _, span := range r.threadSessionSpans {
		if span != nil {
			spans = append(spans, span)
		}
	}
	r.threadSessionSpans = nil
	r.threadSessionSpansMu.Unlock()
	for _, span := range spans {
		span.End()
	}
}

// mcpConnectorInfo reports the connector an MCP call targets (Rust's approval
// metadata connector id/name, recorded on the call span and in its metrics).
func (r *RuntimeRouter) mcpConnectorInfo(invocation *tool.Invocation) (string, string) {
	if r == nil || r.services.ToolRouter == nil || invocation == nil {
		return "", ""
	}
	return r.services.ToolRouter.MCPConnectorInfo(invocation)
}

// emitMCPCallMetrics records Rust's MCP call metric triple for one completed MCP
// call (mcp_tool_call.rs's emit_mcp_call_metrics).
func (r *RuntimeRouter) emitMCPCallMetrics(execution *turn.ToolExecutionResult) {
	if r == nil || execution == nil {
		return
	}
	connectorID, connectorName := r.mcpConnectorInfo(execution.Invocation)
	telemetry.EmitMCPCallMetricsForExecution(r.services.TurnMetrics, execution, connectorID, connectorName)
}
