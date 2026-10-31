package telemetry

import (
	"testing"
	"time"

	"codex_go/tool"
	"codex_go/turn"
)

// Rust #44242 emits MCP call metrics through the issuing step's telemetry, so
// the step model rides along as the `model` metadata tag on the counter,
// duration and error counter.
func TestEmitMCPCallMetricsTagsStepModelLikeRust(t *testing.T) {
	started := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{ToolName: tool.NamespacedName("mcp__example", "shell")},
		Output: &tool.Output{Success: true, Data: map[string]any{
			"mcpToolCall": true,
			"tool":        "shell",
		}},
		TelemetryTags: map[string]string{
			"mcp_server": "example",
			"model":      "gpt-step-2",
		},
		StartedAt:  started,
		FinishedAt: started.Add(10 * time.Millisecond),
	}
	sink := &recordingTurnMetricSink{}
	if !EmitMCPCallMetricsForExecution(sink, execution, "connector-1", "Calendar") {
		t.Fatal("MCP execution was not classified")
	}
	if len(sink.counters) != 1 || len(sink.durations) != 1 {
		t.Fatalf("counters = %#v durations = %#v", sink.counters, sink.durations)
	}
	if got := sink.counters[0].tags["model"]; got != "gpt-step-2" {
		t.Fatalf("counter model tag = %q (%#v)", got, sink.counters[0].tags)
	}
	if got := sink.durations[0].tags["model"]; got != "gpt-step-2" {
		t.Fatalf("duration model tag = %q (%#v)", got, sink.durations[0].tags)
	}

	failed := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{ToolName: tool.NamespacedName("mcp__example", "shell")},
		Output: &tool.Output{Success: false, Data: map[string]any{
			"mcpToolCall":       true,
			"isError":           true,
			"structuredContent": map[string]any{"error_code": "rate_limited"},
		}},
		TelemetryTags: map[string]string{
			"mcp_server": "example",
			"model":      "gpt-step-2",
		},
		StartedAt:  started,
		FinishedAt: started.Add(5 * time.Millisecond),
	}
	errorSink := &recordingTurnMetricSink{}
	if !EmitMCPCallMetricsForExecution(errorSink, failed, "", "") {
		t.Fatal("failed MCP execution was not classified")
	}
	if len(errorSink.counters) != 2 || errorSink.counters[1].tags["error_code"] != "rate_limited" {
		t.Fatalf("error counters = %#v", errorSink.counters)
	}
	if got := errorSink.counters[1].tags["model"]; got != "gpt-step-2" {
		t.Fatalf("error counter model tag = %q (%#v)", got, errorSink.counters[1].tags)
	}

	// Executions that carry no step model keep the previous tag set.
	plain := &turn.ToolExecutionResult{
		Invocation:    &tool.Invocation{ToolName: tool.NamespacedName("mcp__example", "shell")},
		Output:        &tool.Output{Success: true, Data: map[string]any{"mcpToolCall": true}},
		TelemetryTags: map[string]string{"mcp_server": "example"},
		StartedAt:     started,
		FinishedAt:    started,
	}
	plainSink := &recordingTurnMetricSink{}
	EmitMCPCallMetricsForExecution(plainSink, plain, "", "")
	if _, ok := plainSink.counters[0].tags["model"]; ok {
		t.Fatalf("model tag leaked without a step model: %#v", plainSink.counters[0].tags)
	}
}
