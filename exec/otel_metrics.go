package exec

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"codex_go/config"
	"codex_go/doctor"
	"codex_go/otelinit"
	"codex_go/protocol"
	"codex_go/telemetry"
	"codex_go/tool"
	"codex_go/turn"
)

// configureOtelProvider mirrors the exec crate's startup wiring
// (codex-rs/exec/src/lib.rs): build an OtelProvider from the effective config
// with the process originator as the service name and analytics enabled by
// default, record the process start once, and keep the provider so its metrics
// client can be installed on the model runner and shut down at the end of the
// run. Metrics stay inert unless an OTLP metrics exporter is configured (the
// default Statsig route is off in development builds).
func (r *Runner) configureOtelProvider(cfg *config.Config, req *Request) {
	if r == nil || cfg == nil {
		return
	}
	provider, err := otelinit.BuildProvider(otelinit.Options{
		Config:                  cfg,
		ServiceName:             execAgentOriginator(req),
		ServiceVersion:          doctor.Version(),
		DefaultAnalyticsEnabled: true,
	})
	if err != nil {
		slog.Warn("failed to build the OTEL provider", "error", err)
		return
	}
	if provider == nil || provider.Metrics() == nil {
		// The provider may still carry the logging/tracing pipelines used by the
		// session telemetry records.
		if provider != nil {
			r.otelProvider = provider
		}
		r.otelOriginator = execAgentOriginator(req)
		r.otelToolResultLimits = cfg.Otel().ToolResult
		return
	}
	r.otelProvider = provider
	r.otelOriginator = execAgentOriginator(req)
	r.otelToolResultLimits = cfg.Otel().ToolResult
	// Rust's exec records the process-start counter with the "codex_exec"
	// originator.
	telemetry.RecordProcessStartOnce(provider.Metrics(), "codex_exec")
}

// otelMetricsSink returns the run's metrics client when OTEL metrics are
// enabled.
func (r *Runner) otelMetricsSink() *telemetry.MetricsClient {
	if r == nil {
		return nil
	}
	return r.otelProvider.Metrics()
}

// sessionTelemetryForRun builds the run's session telemetry: the metadata the
// run knows (conversation identity, model, originator) with the provider's log
// client bound to the diagnostic records.
func (r *Runner) sessionTelemetryForRun() *telemetry.SessionTelemetry {
	if r == nil || r.otelProvider == nil {
		return nil
	}
	session := telemetry.NewSessionTelemetry(telemetry.SessionTelemetryMetadata{
		AppVersion: doctor.Version(),
		Originator: strings.TrimSpace(r.otelOriginator),
	})
	if client := r.otelProvider.Logs(); client != nil {
		session.Logs = client
	}
	session.Tracer = r.otelProvider.Tracer()
	return session
}

// shutdownOtelProvider flushes and stops the run's provider.
func (r *Runner) shutdownOtelProvider(ctx context.Context) {
	if r == nil {
		return
	}
	provider := r.otelProvider
	if provider == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_ = provider.Shutdown(ctx)
}

// emitTurnMetrics mirrors the session metrics Rust records when a turn
// completes (codex-otel's SessionTelemetry via the turn-completion path): the
// per-model token-usage histogram, the per-turn tool-call count, the
// per-tool-call counter/duration pair, and the unified-exec running-process
// count. The end-to-end duration is emitted separately because it also covers
// failed and interrupted turns.
//
// Go's exec has no memories or managed-network-proxy subsystem, so Rust's
// codex.turn.memory and codex.turn.network_proxy counters are not emitted here.
func (r *Runner) emitTurnMetrics(result *turn.AgentLoopResult, threadID string, modelID string, memoryToolEnabled bool) {
	sink := r.otelMetricsSink()
	if sink == nil || result == nil {
		return
	}
	telemetry.EmitTurnTokenUsageMetrics(sink, result.ModelResponses(), modelID, memoryToolEnabled)
	telemetry.EmitTurnToolCallMetric(sink, len(result.ToolExecutions), memoryToolEnabled)
	for index := range result.ToolExecutions {
		telemetry.EmitToolCallMetric(sink, &result.ToolExecutions[index])
	}
	telemetry.EmitTurnRunningProcessesMetric(sink, r.runningUnifiedExecProcesses(threadID))
	r.emitToolResultRecords(result, threadID, modelID)
}

// emitToolResultRecords mirrors the record half of Rust's
// SessionTelemetry::tool_result_with_tags for the exec runtime: the diagnostic
// log record and the trace-safe span event for every completed call, beside the
// metrics emitTurnMetrics records.
func (r *Runner) emitToolResultRecords(result *turn.AgentLoopResult, threadID string, modelID string) {
	if r == nil || result == nil {
		return
	}
	session := telemetry.NewSessionTelemetry(telemetry.SessionTelemetryMetadata{
		ConversationID: strings.TrimSpace(threadID),
		AppVersion:     doctor.Version(),
		Model:          strings.TrimSpace(modelID),
		Slug:           strings.TrimSpace(modelID),
		Originator:     strings.TrimSpace(r.otelOriginator),
	})
	session.Logs = r.otelProvider.Logs()
	if session.Logs == nil {
		return
	}
	limits := r.otelToolResultLimits
	if limits.MaxBytes <= 0 {
		limits = protocol.DefaultToolResultLogConfig()
	}
	for index := range result.ToolExecutions {
		telemetry.EmitToolResult(context.Background(), session, limits, toolResultEvent(&result.ToolExecutions[index]))
	}
}

// toolResultEvent maps one completed call onto the tool-result event.
func toolResultEvent(execution *turn.ToolExecutionResult) telemetry.ToolResultEvent {
	if execution == nil || execution.Invocation == nil {
		return telemetry.ToolResultEvent{}
	}
	invocation := execution.Invocation
	event := telemetry.ToolResultEvent{
		ToolName:      strings.TrimSpace(invocation.ToolName.Name),
		ToolNamespace: strings.TrimSpace(invocation.ToolName.Namespace),
		CallID:        invocation.CallID,
		Arguments:     execToolLogPayload(invocation),
		Duration:      execution.FinishedAt.Sub(execution.StartedAt),
	}
	if event.Duration < 0 {
		event.Duration = 0
	}
	if execution.Output != nil {
		event.Success = execution.Output.Success
		event.Output = execution.Output.Body
		if event.Output == "" {
			event.Output = execution.Output.Error
		}
	}
	event.MCPServer = strings.TrimSpace(execution.TelemetryTags["mcp_server"])
	event.MCPServerOrigin = strings.TrimSpace(execution.TelemetryTags["mcp_server_origin"])
	return event
}

// execToolLogPayload mirrors codex-tools' ToolPayload::log_payload plus core's
// tool_log_payload: a direct plaintext collaboration call hides its arguments.
func execToolLogPayload(invocation *tool.Invocation) string {
	if invocation == nil {
		return ""
	}
	if invocation.Source == "direct_plaintext_message" {
		return "[plaintext arguments]"
	}
	switch invocation.Payload.Kind {
	case tool.PayloadToolSearch:
		query, _ := invocation.Payload.Search["query"].(string)
		return query
	case tool.PayloadCustom:
		return invocation.Payload.Input
	default:
		return invocation.Payload.Arguments
	}
}

// emitTurnE2EDuration records the turn task's wall-clock duration (recorded for
// every terminal outcome, like Rust's turn timer).
func (r *Runner) emitTurnE2EDuration(startedAt time.Time) {
	sink := r.otelMetricsSink()
	if sink == nil {
		return
	}
	telemetry.EmitTurnE2EDurationMetric(sink, time.Since(startedAt).Milliseconds())
}

func (r *Runner) runningUnifiedExecProcesses(threadID string) int {
	if r == nil || r.UnifiedExec == nil {
		return 0
	}
	return len(r.UnifiedExec.ListProcesses(strings.TrimSpace(threadID)))
}
