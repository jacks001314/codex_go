package turn

import (
	"context"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// Rust's CodeModeToolOutput inserts its status header as the body's first
// content item (core/src/tools/code_mode/output.rs), so the model sees the
// status, wall time and "Output:" marker ahead of the script's own items.

func codeModeExecResponseBody(t *testing.T, results []ToolExecutionResult) any {
	t.Helper()
	if len(results) != 1 || results[0].Response == nil || results[0].Response.Output == nil {
		t.Fatalf("results = %#v", results)
	}
	return results[0].Response.Output.Body
}

func assertHeaderLeadsContentItems(t *testing.T, body any, statusPrefix string) {
	t.Helper()
	items, ok := body.([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("body = %#v, want a content-item list", body)
	}
	first, ok := items[0].(map[string]any)
	if !ok || first["type"] != "input_text" {
		t.Fatalf("first item = %#v, want the header text item", items[0])
	}
	header, _ := first["text"].(string)
	if !strings.HasPrefix(header, statusPrefix) || !strings.Contains(header, "\nWall time ") || !strings.HasSuffix(header, "Output:\n") {
		t.Fatalf("header = %q", header)
	}
}

func TestCodeModeExecHeaderLeadsContentItemsLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	exec, _ := tool.NewCodeModeExecutors(registry)
	if err := registry.Register(exec); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry), ToolMode: model.ToolModeCodeMode})
	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		Type: "custom_tool_call", CallID: "exec-call", Name: tool.CodeModeExecToolName,
		Input: `text("hello"); text("world");`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	assertHeaderLeadsContentItems(t, codeModeExecResponseBody(t, results), "Script completed")
	// Rust joins text items with a newline, so the header's trailing newline
	// leaves a blank line before the script's first item.
	if text := results[0].Response.Output.Text(); !strings.Contains(text, "Output:\n\nhello\nworld") {
		t.Fatalf("output text = %q", text)
	}
}

func TestCodeModeYieldedHeaderLeadsContentItemsLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	exec, wait := tool.NewCodeModeExecutors(registry)
	if err := registry.Register(exec); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(wait); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry), ToolMode: model.ToolModeCodeMode})
	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		Type: "custom_tool_call", CallID: "exec-yield", Name: tool.CodeModeExecToolName,
		Input: `yield_control(); text("later");`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if cellID, _ := results[0].Output.Data["cell_id"].(string); cellID == "" {
		t.Fatalf("yielded output = %#v", results[0].Output)
	}
	assertHeaderLeadsContentItems(t, codeModeExecResponseBody(t, results), "Script running with cell ID ")
}
