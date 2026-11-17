package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/tool"
	"codex_go/turn"
)

// Rust #61257 includes the MCP registration scope on the v2 mcpToolCall item
// and omits it for a local registration.
func TestMCPToolCallThreadItemSerializesEnvironmentScopeLikeRust(t *testing.T) {
	item := &ThreadItem{
		ID:   "call-1",
		Type: "mcpToolCall",
		Data: map[string]any{"server": "notes", "tool": "lookup", "status": "completed", "environmentId": "env-b"},
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"environmentId":"env-b"`) {
		t.Fatalf("wire item = %s", encoded)
	}

	local := &ThreadItem{ID: "call-2", Type: "mcpToolCall", Data: map[string]any{"server": "notes", "tool": "lookup"}}
	encoded, err = json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "environmentId") {
		t.Fatalf("local wire item gained an environment scope: %s", encoded)
	}
}

// TestMCPToolCallRecordKeepsEnvironmentScopeLikeRust pins the record side of
// #61257: the executor's registration scope must reach the persisted session
// item, otherwise resume and canonical rollout lose the environment label even
// though the live v2 wire item carries it.
func TestMCPToolCallRecordKeepsEnvironmentScopeLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "call-scoped",
			ToolName: tool.NamespacedName("mcp__notes__lookup", "lookup"),
			Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"q":"x"}`},
		},
		Output: &tool.Output{
			CallID:   "call-scoped",
			ToolName: tool.NamespacedName("mcp__notes__lookup", "lookup"),
			Success:  true,
			Body:     "ok",
			Data: map[string]any{
				"mcpToolCall":   true,
				"server":        "notes",
				"tool":          "lookup",
				"environmentId": "env-b",
			},
			CompletedAt: now,
		},
		StartedAt:  now,
		FinishedAt: now,
	}

	item, ok := sessionItemForAppToolCall("turn-1", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForAppToolCall() did not produce an item")
	}
	if item.Data["environmentId"] != "env-b" {
		t.Fatalf("recorded environment scope = %#v", item.Data["environmentId"])
	}

	execution.Output.Data = map[string]any{"mcpToolCall": true, "server": "notes", "tool": "lookup"}
	local, ok := sessionItemForAppToolCall("turn-1", execution, now, nil)
	if !ok {
		t.Fatal("sessionItemForAppToolCall() did not produce a local item")
	}
	if _, present := local.Data["environmentId"]; present {
		t.Fatalf("local MCP call gained an environment scope: %#v", local.Data)
	}
}
