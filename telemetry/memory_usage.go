package telemetry

import (
	"encoding/json"
	"strings"

	"codex_go/memories"
	"codex_go/tool"
	"codex_go/turn"
)

// Rust parity: codex-rs/memories/read/src/metrics.rs's MEMORIES_USAGE_METRIC and
// codex-rs/core/src/memory_usage.rs's emit_metric_for_tool_read, which turns a
// completed shell call into one counter per memory artifact it read.

const (
	// MemoryUsageMetricName is Rust's MEMORIES_USAGE_METRIC.
	MemoryUsageMetricName = "codex.memories.usage"
	// MemoryUsageVersionTag names the memory root the artifact lives in
	// (`v1` or `v2`).
	MemoryUsageVersionTag = "memory_version"
)

// MemoryUsageExecCommandParams is Rust's ExecCommandArgs: the unified-exec
// command the read classification walks.
type MemoryUsageExecCommandParams struct {
	Cmd string `json:"cmd"`
}

// MemoryUsageShellScriptForInvocation mirrors Rust's shell_script_for_invocation:
// only a default-namespace `exec_command` call carries a shell script. A
// namespaced call (an MCP server, a connector) never reports memory usage.
func MemoryUsageShellScriptForInvocation(invocation *tool.Invocation) (string, bool) {
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return "", false
	}
	if invocation.ToolName.Namespace != "" || invocation.ToolName.Name != "exec_command" {
		return "", false
	}
	var params MemoryUsageExecCommandParams
	if err := json.Unmarshal([]byte(defaultJSON(invocation.Payload.Arguments)), &params); err != nil || params.Cmd == "" {
		return "", false
	}
	return params.Cmd, true
}

// MemoryUsageTagSets builds Rust's tag list for one completed call: the artifact
// kind, the memory root version, the flat tool name, and whether the call
// succeeded. Rust emits one counter per artifact the script read, in script
// order and without deduplication.
func MemoryUsageTagSets(command string, invocation *tool.Invocation, success bool) []map[string]string {
	usages := memories.UsageFromCommand(command)
	if len(usages) == 0 {
		return nil
	}
	successTag := "false"
	if success {
		successTag = "true"
	}
	toolName := ""
	if invocation != nil {
		toolName = MemoryUsageFlatToolName(invocation.ToolName)
	}
	tags := make([]map[string]string, 0, len(usages))
	for _, usage := range usages {
		tags = append(tags, map[string]string{
			"kind":                string(usage.Kind),
			MemoryUsageVersionTag: string(usage.Version),
			"tool":                toolName,
			"success":             successTag,
		})
	}
	return tags
}

// MemoryUsageFlatToolName reports the tool name Rust tags the counter with
// (codex-tools' flat_tool_name: the namespaced key, unqualified for a default
// namespace tool).
func MemoryUsageFlatToolName(name tool.ToolName) string {
	if name.Namespace == "" {
		return name.Name
	}
	return name.Namespace + "." + name.Name
}

// EmitMemoryUsageMetricsForExecution records Rust's `codex.memories.usage`
// counters for one completed call. It reports false when the call read no memory
// artifact, so a caller can skip the extra work.
func EmitMemoryUsageMetricsForExecution(sink TurnMetricSink, execution *turn.ToolExecutionResult) bool {
	if sink == nil || execution == nil || execution.Invocation == nil {
		return false
	}
	command, ok := MemoryUsageShellScriptForInvocation(execution.Invocation)
	if !ok {
		return false
	}
	success := execution.Output != nil && execution.Output.Success
	tagSets := MemoryUsageTagSets(command, execution.Invocation, success)
	for _, tags := range tagSets {
		sink.Counter(MemoryUsageMetricName, 1, tags)
	}
	return len(tagSets) > 0
}

func defaultJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	return value
}
