package exec

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/tool"
	"codex_go/turn"
)

// Rust #61257 carries the MCP registration scope on the tool-call record for an
// environment-owned server, and leaves it absent for a local registration.
func TestMCPToolCallProtocolItemCarriesEnvironmentScopeLikeRust(t *testing.T) {
	invocation := &tool.Invocation{ToolName: tool.ToolName{Namespace: "mcp__notes", Name: "lookup"}}
	item := mcpToolCallProtocolItem(&turn.ToolExecutionResult{
		Invocation: invocation,
		Output: &tool.Output{
			Success: true,
			Data: map[string]any{
				"mcpToolCall":   true,
				"server":        "notes",
				"tool":          "lookup",
				"environmentId": "env-b",
			},
		},
	}, "completed")
	if item.ServerEnvironmentID == nil || *item.ServerEnvironmentID != "env-b" {
		t.Fatalf("environment scope = %v, want env-b", item.ServerEnvironmentID)
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"environmentId":"env-b"`) {
		t.Fatalf("wire item = %s", encoded)
	}

	local := mcpToolCallProtocolItem(&turn.ToolExecutionResult{
		Invocation: invocation,
		Output: &tool.Output{
			Success: true,
			Data:    map[string]any{"mcpToolCall": true, "server": "notes", "tool": "lookup"},
		},
	}, "completed")
	encoded, err = json.Marshal(local)
	if err != nil {
		t.Fatal(err)
	}
	if local.ServerEnvironmentID != nil || strings.Contains(string(encoded), "environmentId") {
		t.Fatalf("local item gained an environment scope: %s", encoded)
	}
}
