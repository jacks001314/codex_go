package appserver

import (
	"sort"
	"strings"

	"codex_go/model"
	"codex_go/telemetry"
)

// turnTokenUsage is one model's accumulated token usage for a turn.
type turnTokenUsage struct {
	model string
	usage model.AgentUsage
}

// turnTokenUsageByModel mirrors Rust TurnTokenUsage (#44656,
// codex-rs/core/src/state/turn_token_usage.rs): every response contributes its
// usage to the model that resolved to produce it, so a turn that switches
// models (or mixes compaction and response models) reports one sample per
// model and token type instead of one session-level total. Results are sorted
// by model name to mirror Rust's BTreeMap ordering.
func turnTokenUsageByModel(responses []*model.AgentResponse, fallbackModel string) []turnTokenUsage {
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
		addAgentUsage(total, response.Usage)
	}
	names := make([]string, 0, len(byModel))
	for name := range byModel {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]turnTokenUsage, 0, len(names))
	for _, name := range names {
		out = append(out, turnTokenUsage{model: name, usage: *byModel[name]})
	}
	return out
}

func addAgentUsage(total *model.AgentUsage, usage model.AgentUsage) {
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

// emitTurnTokenUsageMetrics records the codex.turn.token_usage histogram, one
// sample per model and token type (Rust #44656). Rust attaches the model as a
// telemetry resource attribute; Go's local metric sink has no resource layer,
// so the model is carried as a "model" tag. The token_type tag values and their
// order mirror Rust; the tmp_mem_enabled tag mirrors the Rust Feature::MemoryTool
// gate.
func (r *RuntimeRouter) emitTurnTokenUsageMetrics(sink telemetry.TurnMetricSink, responses []*model.AgentResponse, fallbackModel string, tmpMemoryEnabled bool) {
	if sink == nil {
		return
	}
	perModel := turnTokenUsageByModel(responses, fallbackModel)
	if len(perModel) == 0 {
		// Preserve a zero-valued completion sample for turns without reported
		// usage (Rust pushes the fallback session telemetry with default usage).
		perModel = []turnTokenUsage{{model: strings.TrimSpace(fallbackModel)}}
	}
	tmpMem := "false"
	if tmpMemoryEnabled {
		tmpMem = "true"
	}
	for _, entry := range perModel {
		samples := [...]struct {
			tokenType string
			value     int64
		}{
			{telemetry.TurnTokenTypeTotal, entry.usage.TotalTokens},
			{telemetry.TurnTokenTypeInput, entry.usage.InputTokens},
			{telemetry.TurnTokenTypeCachedInput, entry.usage.CachedInputTokens},
			{telemetry.TurnTokenTypeCacheWriteInput, entry.usage.CacheWriteInputTokens},
			{telemetry.TurnTokenTypeOutput, entry.usage.OutputTokens},
			{telemetry.TurnTokenTypeReasoningOutput, entry.usage.ReasoningOutputTokens},
		}
		for _, sample := range samples {
			value := sample.value
			if value < 0 {
				value = 0
			}
			sampleTags := map[string]string{
				telemetry.TurnTokenTypeTag: sample.tokenType,
				telemetry.TurnTmpMemoryTag: tmpMem,
			}
			if entry.model != "" {
				sampleTags["model"] = entry.model
			}
			sink.Histogram(telemetry.TurnTokenUsageMetric, int(value), sampleTags)
		}
	}
}

// emitTurnMemoryMetric records the codex.turn.memory counter (Rust #44656,
// emit_turn_memory_metric): whether memory reads were allowed, whether the
// feature/config gates were on, and whether the turn cited memory.
func (r *RuntimeRouter) emitTurnMemoryMetric(sink telemetry.TurnMetricSink, featureEnabled bool, configUseMemories bool, hasCitations bool) {
	if sink == nil {
		return
	}
	readAllowed := featureEnabled && configUseMemories
	sink.Counter(telemetry.TurnMemoryMetric, 1, map[string]string{
		"read_allowed":        boolTagValue(readAllowed),
		"feature_enabled":     boolTagValue(featureEnabled),
		"config_use_memories": boolTagValue(configUseMemories),
		"has_citations":       boolTagValue(hasCitations),
	})
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
	if sink == nil {
		return
	}
	if toolCalls < 0 {
		toolCalls = 0
	}
	sink.Histogram(telemetry.TurnToolCallMetric, toolCalls, map[string]string{
		telemetry.TurnTmpMemoryTag: boolTagValue(tmpMemoryEnabled),
	})
}

// emitTurnNetworkProxyMetric records the codex.turn.network_proxy counter with
// the turn's managed-network active state (Rust #44656).
func (r *RuntimeRouter) emitTurnNetworkProxyMetric(sink telemetry.TurnMetricSink, active bool, tmpMemoryEnabled bool) {
	if sink == nil {
		return
	}
	sink.Counter(telemetry.TurnNetworkProxyMetric, 1, map[string]string{
		"active":                   boolTagValue(active),
		telemetry.TurnTmpMemoryTag: boolTagValue(tmpMemoryEnabled),
	})
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
	sink.Histogram(telemetry.TurnRunningProcessesMetric, count, nil)
}
