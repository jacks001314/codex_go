package app

import (
	"context"
	"strings"
	"testing"

	"codex_go/appserver"
)

// Rust #63184: the TUI-hosted MCP server lists `open_in_codex` with a path and
// an optional one-based line, and the task-tool namespace does not carry it.
func TestOpenInCodexToolSpecLikeRust(t *testing.T) {
	for _, tool := range DynamicToolSpecs()[0].Namespace.Tools {
		if tool.Name == "open_in_codex" {
			t.Fatal("open_in_codex must be hosted by the MCP server, not listed in the task namespace")
		}
	}
	spec := openInCodexToolSpec()
	if spec["name"] != "open_in_codex" || !strings.Contains(spec["description"].(string), "sidebar") {
		t.Fatalf("spec = %#v", spec)
	}
	schema, _ := spec["inputSchema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	path, _ := properties["path"].(map[string]any)
	if path["type"] != "string" || path["minLength"] != 1 {
		t.Fatalf("path schema = %#v", properties["path"])
	}
	line, _ := properties["line"].(map[string]any)
	if line["type"] != "integer" || line["minimum"] != 1 {
		t.Fatalf("line schema = %#v", properties["line"])
	}
	required, _ := schema["required"].([]string)
	if len(required) != 1 || required[0] != "path" {
		t.Fatalf("required = %#v", schema["required"])
	}
}

// The call hands the resolved request to the TUI and returns once it is queued.
func TestOpenInCodexToolQueuesPreviewLikeRust(t *testing.T) {
	var captured struct {
		threadID string
		path     string
		line     int
	}
	called := false
	response := ExecuteDynamicTool(context.Background(), nil, appserver.DynamicToolCallParams{
		ThreadID:  "thread-a",
		Tool:      "open_in_codex",
		Arguments: map[string]any{"path": " src/main.go ", "line": 3},
	}, DynamicToolOptions{OpenInCodex: func(_ context.Context, threadID string, path string, line int) error {
		called = true
		captured.threadID, captured.path, captured.line = threadID, path, line
		return nil
	}})
	if !response.Success {
		t.Fatalf("response = %#v", response)
	}
	if !called || captured.threadID != "thread-a" || captured.path != "src/main.go" || captured.line != 3 {
		t.Fatalf("captured = %#v called=%v", captured, called)
	}
}

// A transport without a sidebar, a blank path, and a non-positive line are
// rejected instead of silently doing nothing.
func TestOpenInCodexToolRejectionsLikeRust(t *testing.T) {
	unavailable := ExecuteDynamicTool(context.Background(), nil, appserver.DynamicToolCallParams{
		ThreadID:  "thread-a",
		Tool:      "open_in_codex",
		Arguments: map[string]any{"path": "src/main.go"},
	}, DynamicToolOptions{})
	if unavailable.Success || !strings.Contains(dynamicToolText(t, unavailable), "unavailable") {
		t.Fatalf("unavailable response = %#v", unavailable)
	}

	for name, arguments := range map[string]map[string]any{
		"blank path": {"path": "   "},
		"bad line":   {"path": "src/main.go", "line": 0},
	} {
		response := ExecuteDynamicTool(context.Background(), nil, appserver.DynamicToolCallParams{
			ThreadID:  "thread-a",
			Tool:      "open_in_codex",
			Arguments: arguments,
		}, DynamicToolOptions{OpenInCodex: func(context.Context, string, string, int) error { return nil }})
		if response.Success {
			t.Fatalf("%s: response = %#v, want a rejection", name, response)
		}
	}
}
