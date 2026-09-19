package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/compact"
	"codex_go/model"
	"codex_go/tool"
	"codex_go/turn"
)

// TestAgentCompactRunnerAttachesCodeModeMetadataLikeRust covers Rust #46044:
// the remote compaction prompt carries the thread recorder's pending Code Mode
// inventory.
func TestAgentCompactRunnerAttachesCodeModeMetadataLikeRust(t *testing.T) {
	recorder := turn.NewExecutedToolCallRecorder()
	recorder.RecordToolCall(&tool.Invocation{
		CallID: "exec-call", ToolName: tool.PlainName(tool.CodeModeExecToolName),
		Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `text("running")`},
	}, model.ToolModeCodeMode)
	recorder.RecordToolCall(&tool.Invocation{
		CallID:   "nested-1",
		ToolName: tool.NamespacedName("mcp", "echo"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message":"first"}`},
		Source:   "code_mode",
		Context:  map[string]any{tool.CodeModeCellIDContextKey: "cell-1"},
	}, model.ToolModeCodeMode)
	recorder.RegisterCell("cell-1", "exec-call")

	agent := &compactReasoningEffortAgent{}
	runner := &agentCompactRunner{
		agent:                           agent,
		model:                           "gpt-5",
		executedToolCalls:               recorder,
		executedToolCallMetadataEnabled: true,
	}
	request := &compact.Request{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Trigger:  compact.TriggerManual,
		Reason:   compact.ReasonUserRequested,
		Phase:    compact.PhaseStandaloneTurn,
		History: []compact.Item{
			{
				Type:   "custom_tool_call",
				CallID: "exec-call",
				Raw:    json.RawMessage(`{"type":"custom_tool_call","call_id":"exec-call","name":"exec","input":"text(\"running\")"}`),
			},
			{
				Type:   "custom_tool_call_output",
				CallID: "exec-call",
				Raw:    json.RawMessage(`{"type":"custom_tool_call_output","call_id":"exec-call","output":"running"}`),
			},
		},
	}
	if _, err := runner.Compact(context.Background(), request); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("agent requests = %d", len(agent.requests))
	}
	found := false
	for _, item := range agent.requests[0].InputItems {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if strings.Contains(string(encoded), "executed_tool_calls") && strings.Contains(string(encoded), "mcp__echo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("compaction prompt is missing the Code Mode inventory: %s", inputItemsJSON(t, agent.requests[0].InputItems))
	}
}

func inputItemsJSON(t *testing.T, items []any) string {
	t.Helper()
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return string(encoded)
}
