package telemetry

import (
	"reflect"
	"testing"

	"codex_go/state"
	"codex_go/tool"
)

func TestMemoryUsageKindsFromCommand(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []MemoryUsageKind
	}{
		{name: "search", command: "codex memories search project plan", want: []MemoryUsageKind{MemoryUsageKindSearch}},
		{name: "read", command: "memory read user/preferences", want: []MemoryUsageKind{MemoryUsageKindRead}},
		{name: "list", command: "memories list", want: []MemoryUsageKind{MemoryUsageKindList}},
		{name: "write", command: "codex memory add remember this", want: []MemoryUsageKind{MemoryUsageKindWrite, MemoryUsageKindAdHocNote}},
		{name: "memory file read", command: "cat ~/.codex/memories/MEMORY.md", want: []MemoryUsageKind{MemoryUsageKindRead}},
		{name: "none", command: "rg -n memory_usage", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MemoryUsageKindsFromCommand(tc.command); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("MemoryUsageKindsFromCommand() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestMemoryUsageShellScriptForInvocation(t *testing.T) {
	shell := &tool.Invocation{
		ToolName: tool.PlainName("shell_command"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"command":"memories list"}`},
	}
	if got, ok := MemoryUsageShellScriptForInvocation(shell); !ok || got != "memories list" {
		t.Fatalf("MemoryUsageShellScriptForInvocation(shell) = %q/%v", got, ok)
	}
	exec := &tool.Invocation{
		ToolName: tool.PlainName("exec_command"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"codex memories search x"}`},
	}
	if got, ok := MemoryUsageShellScriptForInvocation(exec); !ok || got != "codex memories search x" {
		t.Fatalf("MemoryUsageShellScriptForInvocation(exec) = %q/%v", got, ok)
	}
	other := &tool.Invocation{ToolName: tool.PlainName("view_image"), Payload: tool.Payload{Kind: tool.PayloadFunction}}
	if _, ok := MemoryUsageShellScriptForInvocation(other); ok {
		t.Fatalf("MemoryUsageShellScriptForInvocation(other) ok = true")
	}
}

func TestEmitMemoryUsageMetricForToolRead(t *testing.T) {
	metrics := state.NewTaskMetrics()
	invocation := &tool.Invocation{
		ToolName: tool.PlainName("exec_command"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"cmd":"codex memories search x"}`},
	}
	EmitTaskMemoryUsageMetricForToolRead(invocation, true, metrics)
	records := metrics.Records()
	if len(records) != 1 {
		t.Fatalf("records = %#v", records)
	}
	if records[0].Name != MemoryUsageMetricName || records[0].Tags["kind"] != string(MemoryUsageKindSearch) || records[0].Tags["success"] != "true" {
		t.Fatalf("record = %#v", records[0])
	}
}
