package app

import (
	"testing"

	"codex_go/internal/appserver"
)

func TestRemoteProtocolCommandExecutionPreservesLifecycle(t *testing.T) {
	started := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id":      "call-1",
		"type":    "commandExecution",
		"command": "Get-ChildItem test.pdf",
		"status":  "inProgress",
	}, false)
	if started.Type != "command_execution" || started.Command != "Get-ChildItem test.pdf" || started.Status != "in_progress" {
		t.Fatalf("started item = %#v", started)
	}

	completed := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id":               "call-1",
		"type":             "commandExecution",
		"command":          "Get-ChildItem test.pdf",
		"status":           "completed",
		"aggregatedOutput": "test.pdf\n",
		"exitCode":         float64(0),
	}, true)
	if completed.Type != "command_execution" || completed.Command != started.Command || completed.Status != "completed" {
		t.Fatalf("completed item = %#v", completed)
	}
	if completed.ExitCode == nil || *completed.ExitCode != 0 || completed.AggregatedOutput == nil || *completed.AggregatedOutput != "test.pdf\n" {
		t.Fatalf("completed output = %#v", completed)
	}
}

func TestRemoteProtocolMCPToolCallPreservesLifecycle(t *testing.T) {
	arguments := map[string]any{"label": "A"}
	started := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id":        "call-mcp",
		"type":      "mcpToolCall",
		"server":    "geogebra",
		"tool":      "geogebra_create_point",
		"status":    "inProgress",
		"arguments": arguments,
	}, false)
	if started.Type != "mcp_tool_call" || started.Server != "geogebra" || started.Tool != "geogebra_create_point" || started.Status != "in_progress" {
		t.Fatalf("started MCP item = %#v", started)
	}
	if started.Arguments == nil {
		t.Fatalf("started MCP arguments missing: %#v", started)
	}

	completed := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id":        "call-mcp",
		"type":      "mcpToolCall",
		"server":    "geogebra",
		"tool":      "geogebra_create_point",
		"status":    "completed",
		"arguments": arguments,
		"result": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "Point A created"}},
		},
	}, true)
	if completed.Type != "mcp_tool_call" || completed.Result == nil || len(completed.Result.Content) != 1 || completed.CallError != nil {
		t.Fatalf("completed MCP item = %#v", completed)
	}
}
