package telemetry

import (
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
	"codex_go/turn"
)

type recordingTurnMetricSink struct {
	counters   []recordedTurnMetric
	histograms []recordedTurnMetric
	durations  []recordedTurnMetric
}

type recordedTurnMetric struct {
	name     string
	value    int
	duration time.Duration
	tags     map[string]string
}

func (s *recordingTurnMetricSink) Counter(name string, inc int, tags map[string]string) {
	s.counters = append(s.counters, recordedTurnMetric{name: name, value: inc, tags: cloneTurnMetricTags(tags)})
}

func (s *recordingTurnMetricSink) Histogram(name string, value int, tags map[string]string) {
	s.histograms = append(s.histograms, recordedTurnMetric{name: name, value: value, tags: cloneTurnMetricTags(tags)})
}

func (s *recordingTurnMetricSink) RecordDuration(name string, duration time.Duration, tags map[string]string) {
	s.durations = append(s.durations, recordedTurnMetric{name: name, duration: duration, tags: cloneTurnMetricTags(tags)})
}

func cloneTurnMetricTags(tags map[string]string) map[string]string {
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	return cloned
}

// Mirrors Rust TurnTokenUsage: one histogram sample per model and token type,
// with a zero-valued fallback sample when the turn reported no usage.
func TestEmitTurnTokenUsageMetricsLikeRust(t *testing.T) {
	sink := &recordingTurnMetricSink{}
	EmitTurnTokenUsageMetrics(sink, []*model.AgentResponse{
		{Model: "gpt-5", Usage: model.AgentUsage{InputTokens: 10, TotalTokens: 12}},
		{Model: "gpt-5-mini", Usage: model.AgentUsage{InputTokens: 5, OutputTokens: 1, TotalTokens: 6}},
	}, "gpt-5", true)
	if len(sink.histograms) != 12 {
		t.Fatalf("histograms = %d, want 12", len(sink.histograms))
	}
	byModel := map[string]map[string]int{}
	for _, sample := range sink.histograms {
		if sample.name != TurnTokenUsageMetric || sample.tags[TurnTmpMemoryTag] != "true" {
			t.Fatalf("sample = %#v", sample)
		}
		if byModel[sample.tags["model"]] == nil {
			byModel[sample.tags["model"]] = map[string]int{}
		}
		byModel[sample.tags["model"]][sample.tags[TurnTokenTypeTag]] = sample.value
	}
	if byModel["gpt-5"][TurnTokenTypeTotal] != 12 || byModel["gpt-5"][TurnTokenTypeInput] != 10 {
		t.Fatalf("gpt-5 samples = %#v", byModel["gpt-5"])
	}
	if byModel["gpt-5-mini"][TurnTokenTypeTotal] != 6 {
		t.Fatalf("gpt-5-mini samples = %#v", byModel["gpt-5-mini"])
	}

	empty := &recordingTurnMetricSink{}
	EmitTurnTokenUsageMetrics(empty, nil, "gpt-5", false)
	if len(empty.histograms) != 6 {
		t.Fatalf("empty histograms = %d, want 6", len(empty.histograms))
	}
	for _, sample := range empty.histograms {
		if sample.value != 0 || sample.tags["model"] != "gpt-5" || sample.tags[TurnTmpMemoryTag] != "false" {
			t.Fatalf("empty sample = %#v", sample)
		}
	}
}

// Mirrors SessionTelemetry::tool_result_with_tags: the counter/duration pair
// tagged by the flat tool name and success, without the trace-only MCP fields.
func TestEmitToolCallMetricLikeRust(t *testing.T) {
	started := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	sink := &recordingTurnMetricSink{}
	EmitToolCallMetric(sink, &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{ToolName: tool.PlainName("shell")},
		Output:     &tool.Output{Success: true},
		TelemetryTags: map[string]string{
			"tool_tag":          "exec",
			"mcp_server":        "calendar",
			"mcp_server_origin": "connector",
		},
		StartedAt:  started,
		FinishedAt: started.Add(25 * time.Millisecond),
	})
	if len(sink.counters) != 1 || sink.counters[0].name != ToolCallCountMetric ||
		sink.counters[0].tags["tool"] != "shell" || sink.counters[0].tags["success"] != "true" ||
		sink.counters[0].tags["tool_tag"] != "exec" {
		t.Fatalf("counters = %#v", sink.counters)
	}
	if _, ok := sink.counters[0].tags["mcp_server"]; ok {
		t.Fatalf("mcp_server leaked into the metric tags: %#v", sink.counters[0].tags)
	}
	if len(sink.durations) != 1 || sink.durations[0].name != ToolCallDurationMetric ||
		sink.durations[0].duration != 25*time.Millisecond {
		t.Fatalf("durations = %#v", sink.durations)
	}
	EmitToolCallMetric(sink, nil)
}

// The remaining turn emitters clamp negative values and use Rust's tags.
func TestEmitTurnMetricFamilyLikeRust(t *testing.T) {
	sink := &recordingTurnMetricSink{}
	EmitTurnMemoryMetric(sink, true, false, true)
	EmitTurnToolCallMetric(sink, -3, true)
	EmitTurnNetworkProxyMetric(sink, false, true)
	EmitTurnRunningProcessesMetric(sink, -1)
	EmitTurnE2EDurationMetric(sink, -5)
	if len(sink.counters) != 2 {
		t.Fatalf("counters = %#v", sink.counters)
	}
	memory := sink.counters[0]
	if memory.name != TurnMemoryMetric || memory.tags["read_allowed"] != "false" ||
		memory.tags["feature_enabled"] != "true" || memory.tags["config_use_memories"] != "false" ||
		memory.tags["has_citations"] != "true" {
		t.Fatalf("memory counter = %#v", memory)
	}
	network := sink.counters[1]
	if network.name != TurnNetworkProxyMetric || network.tags["active"] != "false" || network.tags[TurnTmpMemoryTag] != "true" {
		t.Fatalf("network counter = %#v", network)
	}
	if len(sink.histograms) != 2 || sink.histograms[0].name != TurnToolCallMetric ||
		sink.histograms[0].value != 0 || sink.histograms[1].name != TurnUnifiedExecRunningProcessesMetric ||
		sink.histograms[1].value != 0 {
		t.Fatalf("histograms = %#v", sink.histograms)
	}
	if len(sink.durations) != 1 || sink.durations[0].name != TurnE2EDurationMetric ||
		sink.durations[0].duration != 0 || len(sink.durations[0].tags) != 0 {
		t.Fatalf("durations = %#v", sink.durations)
	}
}
