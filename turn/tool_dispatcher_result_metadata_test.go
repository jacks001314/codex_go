package turn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
)

// Mirrors Rust #46010's dispatcher halves: a direct call attaches the raw result
// metadata its tool exposed to that call's own record, while a nested code-mode
// result is recorded on the pending code-mode call.
func TestToolDispatcherRecordsResultMetadataLikeRust(t *testing.T) {
	meta := map[string]any{"provider/custom": map[string]any{"items": []any{1}}}

	t.Run("direct", func(t *testing.T) {
		registry := tool.NewRegistry()
		if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("apps-tool")}, func(_ context.Context, _ *tool.Invocation) (*tool.Output, error) {
			return &tool.Output{Success: true, Body: "ok", ToolResultMetadata: meta}, nil
		})); err != nil {
			t.Fatalf("register apps-tool: %v", err)
		}
		recorder := NewExecutedToolCallRecorder()
		dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry), ExecutedToolCalls: recorder})
		results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
			Type: "function_call", CallID: "call-direct", Name: "apps-tool", Arguments: `{}`,
		}})
		if err != nil {
			t.Fatalf("ExecuteToolItems() error = %v", err)
		}
		if len(results) != 1 || results[0].Response == nil {
			t.Fatalf("results = %#v", results)
		}
		data, err := json.Marshal(results[0].Response)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if !strings.Contains(string(data), `"tool_result_metadata"`) {
			t.Fatalf("direct output dropped the result metadata: %s", data)
		}
	})

	t.Run("code mode nested", func(t *testing.T) {
		registry := tool.NewRegistry()
		if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName(tool.CodeModeExecToolName)}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
			nested := &tool.Invocation{
				CallID:   "nested-meta",
				Source:   "code_mode",
				ToolName: tool.PlainName("nested-tool"),
				Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
				Context:  map[string]any{tool.CodeModeCellIDContextKey: "cell-meta"},
			}
			startedAt := time.Now().UTC()
			if started, ok := invocation.Context["code_mode_nested_tool_started"].(tool.CodeModeNestedToolStartedFunc); ok {
				started(ctx, nested, startedAt)
			}
			if completed, ok := invocation.Context["code_mode_nested_tool_completed"].(tool.CodeModeNestedToolCompletedFunc); ok {
				completed(ctx, nested, &tool.Output{Success: true, Body: "ok", ToolResultMetadata: meta}, nil, startedAt, startedAt)
			}
			return &tool.Output{Success: true, Body: "cell output", Data: map[string]any{"cell_id": "cell-meta"}}, nil
		})); err != nil {
			t.Fatalf("register exec: %v", err)
		}
		recorder := NewExecutedToolCallRecorder()
		dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry), ExecutedToolCalls: recorder})
		if _, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
			Type: "function_call", CallID: "call-exec", Name: tool.CodeModeExecToolName, Arguments: `{}`,
		}}); err != nil {
			t.Fatalf("ExecuteToolItems() error = %v", err)
		}
		items, attachment := recorder.AttachPendingToPrompt([]any{&ToolResponseItem{
			Type: "function_call_output", CallID: "call-exec", Output: NewFunctionCallOutputPayload("", boolPtr(true)),
		}})
		if attachment == nil || len(items) != 1 {
			t.Fatalf("AttachPendingToPrompt() = %#v, %#v", items, attachment)
		}
		data, err := json.Marshal(items[0])
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if !strings.Contains(string(data), `"tool_result_metadata"`) {
			t.Fatalf("nested code-mode call dropped the result metadata: %s", data)
		}
	})
}
