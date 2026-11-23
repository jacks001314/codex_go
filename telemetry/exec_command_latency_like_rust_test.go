package telemetry

import (
	"testing"
	"time"

	"codex_go/tool"
	"codex_go/turn"
)

// Rust #62507 records paired Statsig counters for the built-in exec_command
// handler only: plain (default-namespace) runs, including failures, measured
// from handler entry. Namespaced, MCP, and other tools stay on the generic
// tool-call metrics alone.
func TestExecCommandLatencyCountersLikeRust(t *testing.T) {
	started := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		toolName tool.ToolName
		success  bool
		want     bool
	}{
		{"plain", tool.PlainName("exec_command"), true, true},
		{"namespaced", tool.NamespacedName("functions", "exec_command"), true, false},
		{"failure", tool.PlainName("exec_command"), false, true},
		{"other", tool.PlainName("write_stdin"), true, false},
		{"mcp", tool.NamespacedName("mcp__example", "exec_command"), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &recordingTurnMetricSink{}
			EmitToolCallMetricWithProductSKU(sink, &turn.ToolExecutionResult{
				Invocation:    &tool.Invocation{ToolName: tc.toolName},
				Output:        &tool.Output{Success: tc.success},
				TelemetryTags: map[string]string{"shell_snapshot_mode": "v2"},
				StartedAt:     started,
				FinishedAt:    started.Add(5 * time.Millisecond),
			}, "codex")

			var duration *recordedTurnMetric
			var calls *recordedTurnMetric
			for index := range sink.counters {
				switch sink.counters[index].name {
				case ExecCommandDurationTotalMetric:
					duration = &sink.counters[index]
				case ExecCommandCountMetric:
					calls = &sink.counters[index]
				}
			}
			if !tc.want {
				if duration != nil || calls != nil {
					t.Fatalf("unexpected exec_command counters: %#v", sink.counters)
				}
			} else {
				if duration == nil || duration.value != 5 {
					t.Fatalf("duration_total_ms = %#v", duration)
				}
				if calls == nil || calls.value != 1 {
					t.Fatalf("calls = %#v", calls)
				}
				for _, sample := range []*recordedTurnMetric{duration, calls} {
					if sample.tags["shell_snapshot_mode"] != "v2" ||
						sample.tags["success"] != TurnMetricBoolTag(tc.success) ||
						sample.tags["product_sku"] != "codex" {
						t.Fatalf("counter tags = %#v", sample.tags)
					}
					if _, ok := sample.tags["tool"]; ok {
						t.Fatalf("exec counter kept the tool tag: %#v", sample.tags)
					}
				}
			}
			// The generic histogram keeps its own tags and never gains product_sku.
			if len(sink.durations) != 1 || sink.durations[0].name != ToolCallDurationMetric {
				t.Fatalf("durations = %#v", sink.durations)
			}
			if sink.durations[0].tags["tool"] != tc.toolName.Key() {
				t.Fatalf("histogram tool tag = %#v", sink.durations[0].tags)
			}
			if _, ok := sink.durations[0].tags["product_sku"]; ok {
				t.Fatalf("histogram gained product_sku: %#v", sink.durations[0].tags)
			}
		})
	}
}

// A host that does not know the session's product SKU omits the tag instead of
// emitting an empty one.
func TestExecCommandLatencyOmitsUnknownProductSKULikeRust(t *testing.T) {
	started := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	sink := &recordingTurnMetricSink{}
	EmitToolCallMetric(sink, &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{ToolName: tool.PlainName("exec_command")},
		Output:     &tool.Output{Success: true},
		StartedAt:  started,
		FinishedAt: started.Add(2 * time.Millisecond),
	})
	for _, sample := range sink.counters {
		if _, ok := sample.tags["product_sku"]; ok {
			t.Fatalf("empty product sku serialized: %#v", sample.tags)
		}
	}
}

// The exec_command counters ride the built-in Statsig route: unlike the generic
// tool-call counter and histogram, they are not on STATSIG_DISABLED_METRICS.
func TestExecCommandLatencyCountersReachStatsigLikeRust(t *testing.T) {
	disabled := map[string]bool{}
	for _, name := range StatsigDisabledMetrics {
		disabled[name] = true
	}
	for _, name := range []string{ExecCommandDurationTotalMetric, ExecCommandCountMetric} {
		if disabled[name] {
			t.Fatalf("%s is Statsig-disabled", name)
		}
	}
	if !disabled[ToolCallCountMetric] || !disabled[ToolCallDurationMetric] {
		t.Fatalf("generic tool metrics should stay Statsig-disabled: %#v", StatsigDisabledMetrics)
	}
}
