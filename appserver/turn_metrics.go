package appserver

import (
	"strings"

	"codex_go/model"
	"codex_go/telemetry"
	"codex_go/turn"
)

// emitTurnTokenUsageMetrics records the codex.turn.token_usage histogram, one
// sample per model and token type (Rust #44656; the shared emitter lives in
// telemetry so the exec runtime reports the same series).
func (r *RuntimeRouter) emitTurnTokenUsageMetrics(sink telemetry.TurnMetricSink, responses []*model.AgentResponse, fallbackModel string, tmpMemoryEnabled bool) {
	telemetry.EmitTurnTokenUsageMetrics(sink, responses, fallbackModel, tmpMemoryEnabled)
}

// emitTurnMemoryMetric records the codex.turn.memory counter (Rust #44656,
// emit_turn_memory_metric): whether memory reads were allowed, whether the
// feature/config gates were on, and whether the turn cited memory.
func (r *RuntimeRouter) emitTurnMemoryMetric(sink telemetry.TurnMetricSink, featureEnabled bool, configUseMemories bool, hasCitations bool) {
	telemetry.EmitTurnMemoryMetric(sink, featureEnabled, configUseMemories, hasCitations)
}

func boolTagValue(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// emitTurnToolCallMetric records the codex.turn.tool.call histogram for the
// turn's dispatched tool calls (Rust #44656 emits turn_state.tool_calls, which
// is incremented for every dispatched invocation).
func (r *RuntimeRouter) emitTurnToolCallMetric(sink telemetry.TurnMetricSink, toolCalls int, tmpMemoryEnabled bool) {
	telemetry.EmitTurnToolCallMetric(sink, toolCalls, tmpMemoryEnabled)
}

// emitToolCallMetrics records Rust's per-tool-call metrics
// (SessionTelemetry::tool_result_with_tags): the codex.tool.call counter and the
// codex.tool.call.duration_ms histogram, tagged by the flat tool name, success,
// and the executor's telemetry tags. mcp_server/mcp_server_origin are trace-only
// fields in Rust, so they stay out of the metric tags.
func (r *RuntimeRouter) emitToolCallMetrics(sink telemetry.TurnMetricSink, execution *turn.ToolExecutionResult) {
	telemetry.EmitToolCallMetric(sink, execution)
}

// emitTurnE2EDurationMetric mirrors Rust's TURN_E2E_DURATION_METRIC timer: the
// wall-clock duration of the turn task, recorded untagged when the task ends
// (success, failure, or interruption).
func (r *RuntimeRouter) emitTurnE2EDurationMetric(sink telemetry.TurnMetricSink, durationMS int64) {
	telemetry.EmitTurnE2EDurationMetric(sink, durationMS)
}

// emitTurnNetworkProxyMetric records the codex.turn.network_proxy counter with
// the turn's managed-network active state (Rust #44656).
func (r *RuntimeRouter) emitTurnNetworkProxyMetric(sink telemetry.TurnMetricSink, active bool, tmpMemoryEnabled bool) {
	telemetry.EmitTurnNetworkProxyMetric(sink, active, tmpMemoryEnabled)
}

// managedNetworkProxyActive reports whether the host's managed network proxy is
// enabled (Rust services.network_proxy.current_cfg().enabled).
func (r *RuntimeRouter) managedNetworkProxyActive() bool {
	if r == nil || r.services.ManagedNetwork == nil {
		return false
	}
	return r.services.ManagedNetwork.RemoteConfigSnapshot().Network.Enabled
}

// emitTurnRunningProcessesMetric records how many unified-exec processes are
// still running for the thread (Rust list_background_terminals().len()). Rust
// emits a counter; Go records the gauge value as a histogram sample so a
// zero-count turn is preserved (state.TaskMetrics.Counter promotes 0 to 1).
func (r *RuntimeRouter) emitTurnRunningProcessesMetric(sink telemetry.TurnMetricSink, threadID string) {
	if sink == nil {
		return
	}
	count := 0
	if r != nil && r.services.UnifiedExec != nil {
		count = len(r.services.UnifiedExec.ListProcesses(strings.TrimSpace(threadID)))
	}
	telemetry.EmitTurnRunningProcessesMetric(sink, count)
}
