package telemetry

import (
	"sort"
	"strings"
	"time"

	"codex_go/model"
	"codex_go/turn"
)

// Turn-scoped metric emission, mirroring the codex-otel SessionTelemetry methods
// the turn completion paths call (codex-rs/core/src/state/turn_token_usage.rs and
// SessionTelemetry::tool_result_with_tags). The app-server and the exec runtime
// share these emitters so both report the same series.

// TurnTokenUsage is one model's accumulated token usage for a turn.
type TurnTokenUsage struct {
	Model string
	Usage model.AgentUsage
}

// TurnTokenUsageByModel mirrors Rust TurnTokenUsage (#44656): every response
// contributes its usage to the model that resolved to produce it, so a turn that
// switches models (or mixes compaction and response models) reports one sample
// per model and token type instead of one session-level total. Results are
// sorted by model name to mirror Rust's BTreeMap ordering.
func TurnTokenUsageByModel(responses []*model.AgentResponse, fallbackModel string) []TurnTokenUsage {
	byModel := map[string]*model.AgentUsage{}
	for _, response := range responses {
		if response == nil {
			continue
		}
		modelName := strings.TrimSpace(response.Model)
		if modelName == "" {
			modelName = strings.TrimSpace(response.ServerModel)
		}
		if modelName == "" {
			modelName = strings.TrimSpace(fallbackModel)
		}
		total := byModel[modelName]
		if total == nil {
			total = &model.AgentUsage{}
			byModel[modelName] = total
		}
		AddAgentUsage(total, response.Usage)
	}
	names := make([]string, 0, len(byModel))
	for name := range byModel {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]TurnTokenUsage, 0, len(names))
	for _, name := range names {
		out = append(out, TurnTokenUsage{Model: name, Usage: *byModel[name]})
	}
	return out
}

// AddAgentUsage accumulates one response's usage into the turn total.
func AddAgentUsage(total *model.AgentUsage, usage model.AgentUsage) {
	if total == nil {
		return
	}
	total.InputTokens += usage.InputTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.CacheWriteInputTokens += usage.CacheWriteInputTokens
	total.OutputTokens += usage.OutputTokens
	total.ReasoningOutputTokens += usage.ReasoningOutputTokens
	total.TotalTokens += usage.TotalTokens
}

// EmitTurnTokenUsageMetrics records the codex.turn.token_usage histogram, one
// sample per model and token type (Rust #44656). Rust attaches the model as a
// telemetry resource attribute; Go's local metric sink has no resource layer, so
// the model is carried as a "model" tag. The token_type tag values and their
// order mirror Rust; the tmp_mem_enabled tag mirrors the Rust Feature::MemoryTool
// gate.
func EmitTurnTokenUsageMetrics(sink TurnMetricSink, responses []*model.AgentResponse, fallbackModel string, tmpMemoryEnabled bool) {
	if sink == nil {
		return
	}
	perModel := TurnTokenUsageByModel(responses, fallbackModel)
	if len(perModel) == 0 {
		// Preserve a zero-valued completion sample for turns without reported
		// usage (Rust pushes the fallback session telemetry with default usage).
		perModel = []TurnTokenUsage{{Model: strings.TrimSpace(fallbackModel)}}
	}
	tmpMem := TurnMetricBoolTag(tmpMemoryEnabled)
	for _, entry := range perModel {
		samples := [...]struct {
			tokenType string
			value     int64
		}{
			{TurnTokenTypeTotal, entry.Usage.TotalTokens},
			{TurnTokenTypeInput, entry.Usage.InputTokens},
			{TurnTokenTypeCachedInput, entry.Usage.CachedInputTokens},
			{TurnTokenTypeCacheWriteInput, entry.Usage.CacheWriteInputTokens},
			{TurnTokenTypeOutput, entry.Usage.OutputTokens},
			{TurnTokenTypeReasoningOutput, entry.Usage.ReasoningOutputTokens},
		}
		for _, sample := range samples {
			value := sample.value
			if value < 0 {
				value = 0
			}
			sampleTags := map[string]string{
				TurnTokenTypeTag: sample.tokenType,
				TurnTmpMemoryTag: tmpMem,
			}
			if entry.Model != "" {
				sampleTags["model"] = entry.Model
			}
			sink.Histogram(TurnTokenUsageMetric, int(value), sampleTags)
		}
	}
}

// EmitTurnMemoryMetric records the codex.turn.memory counter (Rust #44656,
// emit_turn_memory_metric): whether memory reads were allowed, whether the
// feature/config gates were on, and whether the turn cited memory.
func EmitTurnMemoryMetric(sink TurnMetricSink, featureEnabled bool, configUseMemories bool, hasCitations bool) {
	if sink == nil {
		return
	}
	readAllowed := featureEnabled && configUseMemories
	sink.Counter(TurnMemoryMetric, 1, map[string]string{
		"read_allowed":        TurnMetricBoolTag(readAllowed),
		"feature_enabled":     TurnMetricBoolTag(featureEnabled),
		"config_use_memories": TurnMetricBoolTag(configUseMemories),
		"has_citations":       TurnMetricBoolTag(hasCitations),
	})
}

// EmitTurnToolCallMetric records the codex.turn.tool.call histogram for the
// turn's dispatched tool calls (Rust #44656 emits turn_state.tool_calls, which
// is incremented for every dispatched invocation).
func EmitTurnToolCallMetric(sink TurnMetricSink, toolCalls int, tmpMemoryEnabled bool) {
	if sink == nil {
		return
	}
	if toolCalls < 0 {
		toolCalls = 0
	}
	sink.Histogram(TurnToolCallMetric, toolCalls, map[string]string{
		TurnTmpMemoryTag: TurnMetricBoolTag(tmpMemoryEnabled),
	})
}

// EmitToolCallMetric records Rust's per-tool-call metrics
// (SessionTelemetry::tool_result_with_tags): the codex.tool.call counter and the
// codex.tool.call.duration_ms histogram, tagged by the flat tool name, success,
// and the executor's telemetry tags. mcp_server/mcp_server_origin are trace-only
// fields in Rust, so they stay out of the metric tags.
func EmitToolCallMetric(sink TurnMetricSink, execution *turn.ToolExecutionResult) {
	if sink == nil || execution == nil || execution.Invocation == nil {
		return
	}
	tags := make(map[string]string, len(execution.TelemetryTags)+2)
	for key, value := range execution.TelemetryTags {
		if key == "mcp_server" || key == "mcp_server_origin" {
			continue
		}
		tags[key] = value
	}
	tags["tool"] = execution.Invocation.ToolName.Key()
	success := execution.Output != nil && execution.Output.Success
	tags["success"] = TurnMetricBoolTag(success)

	sink.Counter(ToolCallCountMetric, 1, tags)
	duration := execution.FinishedAt.Sub(execution.StartedAt)
	if duration < 0 {
		duration = 0
	}
	sink.RecordDuration(ToolCallDurationMetric, duration, tags)
}

// EmitTurnE2EDurationMetric mirrors Rust's TURN_E2E_DURATION_METRIC timer: the
// wall-clock duration of the turn task, recorded untagged when the task ends
// (success, failure, or interruption).
func EmitTurnE2EDurationMetric(sink TurnMetricSink, durationMS int64) {
	if sink == nil {
		return
	}
	if durationMS < 0 {
		durationMS = 0
	}
	sink.RecordDuration(TurnE2EDurationMetric, time.Duration(durationMS)*time.Millisecond, nil)
}

// EmitTurnNetworkProxyMetric records the codex.turn.network_proxy counter with
// the turn's managed-network active state (Rust #44656).
func EmitTurnNetworkProxyMetric(sink TurnMetricSink, active bool, tmpMemoryEnabled bool) {
	if sink == nil {
		return
	}
	sink.Counter(TurnNetworkProxyMetric, 1, map[string]string{
		"active":         TurnMetricBoolTag(active),
		TurnTmpMemoryTag: TurnMetricBoolTag(tmpMemoryEnabled),
	})
}

// EmitTurnRunningProcessesMetric records how many unified-exec processes are
// still running for the thread (Rust list_background_terminals().len()). Rust
// emits a counter; Go records the gauge value as a histogram sample so a
// zero-count turn is preserved (state.TaskMetrics.Counter promotes 0 to 1).
func EmitTurnRunningProcessesMetric(sink TurnMetricSink, count int) {
	if sink == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	sink.Histogram(TurnUnifiedExecRunningProcessesMetric, count, nil)
}

// TurnMetricBoolTag formats a boolean metric tag value the way Rust does.
func TurnMetricBoolTag(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
