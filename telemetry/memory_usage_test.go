package telemetry

import (
	"testing"

	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
)

func memoryUsageInvocation(toolName tool.ToolName, arguments string) *tool.Invocation {
	return &tool.Invocation{
		ToolName: toolName,
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: arguments},
	}
}

// Rust's shell_script_for_invocation only reads a default-namespace
// exec_command; every other call has no shell script to classify.
func TestMemoryUsageShellScriptForInvocation(t *testing.T) {
	exec := memoryUsageInvocation(tool.PlainName("exec_command"), `{"cmd":"cat /tmp/memories/MEMORY.md"}`)
	if got, ok := MemoryUsageShellScriptForInvocation(exec); !ok || got != "cat /tmp/memories/MEMORY.md" {
		t.Fatalf("MemoryUsageShellScriptForInvocation(exec_command) = %q/%v", got, ok)
	}
	for name, invocation := range map[string]*tool.Invocation{
		"retired shell tool": memoryUsageInvocation(tool.PlainName("shell_command"), `{"command":"cat /tmp/memories/MEMORY.md"}`),
		"namespaced exec":    memoryUsageInvocation(tool.NamespacedName("mcp", "exec_command"), `{"cmd":"cat /tmp/memories/MEMORY.md"}`),
		"other tool":         memoryUsageInvocation(tool.PlainName("view_image"), `{"path":"x"}`),
		"empty command":      memoryUsageInvocation(tool.PlainName("exec_command"), `{"cmd":""}`),
	} {
		if _, ok := MemoryUsageShellScriptForInvocation(invocation); ok {
			t.Fatalf("%s must not report a shell script", name)
		}
	}
	if _, ok := MemoryUsageShellScriptForInvocation(nil); ok {
		t.Fatal("a nil invocation must not report a shell script")
	}
}

// The counter carries Rust's exact tag set: the artifact kind, the memory root
// version, the flat tool name, and whether the call succeeded.
func TestMemoryUsageTagSetsFollowRustTags(t *testing.T) {
	invocation := memoryUsageInvocation(tool.PlainName("exec_command"), "{}")
	command := "cat /tmp/.codex/memories/MEMORY.md && cat /tmp/.codex/memories_v2/skills/x/SKILL.md"
	got := MemoryUsageTagSets(command, invocation, true)
	want := []map[string]string{
		{"kind": "memory_md", "memory_version": "v1", "tool": "exec_command", "success": "true"},
		{"kind": "skills", "memory_version": "v2", "tool": "exec_command", "success": "true"},
	}
	if len(got) != len(want) {
		t.Fatalf("tag sets = %#v, want %#v", got, want)
	}
	for index := range want {
		for key, value := range want[index] {
			if got[index][key] != value {
				t.Fatalf("tag set %d = %#v, want %#v", index, got[index], want[index])
			}
		}
		if len(got[index]) != len(want[index]) {
			t.Fatalf("tag set %d = %#v, want %#v", index, got[index], want[index])
		}
	}
	if failed := MemoryUsageTagSets(command, invocation, false); failed[0]["success"] != "false" {
		t.Fatalf("failed call tags = %#v", failed)
	}
	if namespaced := MemoryUsageTagSets(command, memoryUsageInvocation(tool.NamespacedName("mcp", "exec_command"), "{}"), true); namespaced[0]["tool"] != "mcp.exec_command" {
		t.Fatalf("namespaced tool tag = %#v", namespaced[0])
	}
	if none := MemoryUsageTagSets("git status", invocation, true); none != nil {
		t.Fatalf("unclassified command tags = %#v, want nil", none)
	}
}

// The emitter records one `codex.memories.usage` counter per artifact read and
// reports false for a call that read nothing.
func TestEmitMemoryUsageMetricsForExecution(t *testing.T) {
	metrics := state.NewTaskMetrics()
	execution := &turn.ToolExecutionResult{
		Invocation: memoryUsageInvocation(tool.PlainName("exec_command"),
			`{"cmd":"cat /tmp/.codex/memories/memory_summary.md && cat /tmp/.codex/memories_v2/raw_memories.md"}`),
		Output: &tool.Output{Success: true},
	}
	if !EmitMemoryUsageMetricsForExecution(metrics, execution) {
		t.Fatal("a memory read must report usage")
	}
	records := metrics.Records()
	if len(records) != 2 {
		t.Fatalf("records = %#v", records)
	}
	wantKinds := []string{"memory_summary", "raw_memories"}
	wantVersions := []string{"v1", "v2"}
	for index, record := range records {
		if record.Name != MemoryUsageMetricName ||
			record.Tags["kind"] != wantKinds[index] ||
			record.Tags[MemoryUsageVersionTag] != wantVersions[index] ||
			record.Tags["tool"] != "exec_command" ||
			record.Tags["success"] != "true" {
			t.Fatalf("record %d = %#v", index, record)
		}
	}

	unrelated := &turn.ToolExecutionResult{
		Invocation: memoryUsageInvocation(tool.PlainName("exec_command"), `{"cmd":"go test ./..."}`),
		Output:     &tool.Output{Success: true},
	}
	if EmitMemoryUsageMetricsForExecution(metrics, unrelated) {
		t.Fatal("a non-memory command must not report usage")
	}
	if len(metrics.Records()) != 2 {
		t.Fatalf("unrelated call added records: %#v", metrics.Records())
	}
}
