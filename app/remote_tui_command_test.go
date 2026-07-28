package app

import (
	"strings"
	"testing"

	"codex_go/appserver"
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

func TestRemoteProtocolReviewItemUsesReviewText(t *testing.T) {
	item := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id": "review-turn", "type": "enteredReviewMode", "review": "changes against 'main'",
	}, false)
	if item.Type != "enteredReviewMode" || item.Text != "changes against 'main'" {
		t.Fatalf("review item = %#v", item)
	}
	if strings.TrimSpace(item.Text) == "" {
		t.Fatal("review item text is empty")
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

func TestRemoteProtocolWebSearchPreservesLifecycle(t *testing.T) {
	started := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id":     "call-web",
		"type":   "webSearch",
		"query":  "",
		"action": map[string]any{"type": "other"},
	}, false)
	if started.Type != "web_search" || started.ID != "call-web" || started.Action["type"] != "other" {
		t.Fatalf("started web search item = %#v", started)
	}

	completed := remoteProtocolItemFromPayload(appserver.ThreadItemPayload{
		"id":    "call-web",
		"type":  "webSearch",
		"query": "Yunnan weather",
		"action": map[string]any{
			"type":  "search",
			"query": "Yunnan weather",
		},
	}, true)
	if completed.Type != "web_search" || completed.Query != "Yunnan weather" || completed.Action["type"] != "search" || completed.Action["query"] != "Yunnan weather" {
		t.Fatalf("completed web search item = %#v", completed)
	}
}
