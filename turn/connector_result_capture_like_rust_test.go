package turn

import (
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// Mirrors Rust's direct Apps capture (#62392): an eligible MCP result binds a
// local result id to the executed-tool row, and the turn's request input then
// carries the body without putting it in history.
func TestDispatcherCapturesConnectorResultBodyLikeRust(t *testing.T) {
	recorder := NewExecutedToolCallRecorder()
	recorder.BeginResultBodyTurn("turn-1", "chatgpt|acct", true)
	dispatcher := &ToolDispatcher{executedToolCalls: recorder, turnID: "turn-1"}
	output := &tool.Output{
		Success: true,
		ResultBodySource: &tool.ResultBodySource{
			Content: []any{map[string]any{"type": "text", "text": "connector result"}},
		},
	}
	id, ok := dispatcher.captureConnectorResultBody(output)
	if !ok || id == "" {
		t.Fatalf("captureConnectorResultBody() = %q, %v", id, ok)
	}

	item := &model.AgentItem{Type: "function_call_output", ID: "fco-1"}
	call := model.NewExecutedToolCall("search", map[string]any{"q": "x"})
	call.SetToolResultMetadata(model.NewToolResultMetadata(nil).WithResultID(id))
	item.AppendExecutedToolCalls(call)
	attachments := recorder.ResultBodyAttachments("chatgpt|acct", []any{item})
	if len(attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(attachments))
	}
	if !strings.Contains(attachments[0].Body.JSON(), "connector result") {
		t.Fatalf("body = %s", attachments[0].Body.JSON())
	}

	// An unsuccessful call, or one that exposed no raw result, captures nothing.
	if _, ok := dispatcher.captureConnectorResultBody(&tool.Output{Success: false, ResultBodySource: output.ResultBodySource}); ok {
		t.Fatal("captureConnectorResultBody() captured an unsuccessful call")
	}
	if _, ok := dispatcher.captureConnectorResultBody(&tool.Output{Success: true}); ok {
		t.Fatal("captureConnectorResultBody() captured a call without a raw result")
	}
	// A turn whose owner is unset captures nothing at all.
	unowned := &ToolDispatcher{executedToolCalls: NewExecutedToolCallRecorder(), turnID: "turn-1"}
	if _, ok := unowned.captureConnectorResultBody(output); ok {
		t.Fatal("captureConnectorResultBody() captured without a turn store")
	}
}

// Mirrors Rust PreparedMcpCall::permits_result_body gating (#62392): the
// passthrough feature and a resolvable server config are both required.
func TestMCPResultBodyCaptureRequiresFeatureAndServerLikeRust(t *testing.T) {
	if mcpResultBodyCaptureAllowed(&ToolRegistryOptions{}, "codex_apps") {
		t.Fatal("capture allowed without a capture permission")
	}
	options := &ToolRegistryOptions{MCPResultBodyCapture: &McpResultBodyCapture{
		Enabled:         true,
		SessionAuthMode: "chatgpt",
		RuntimeAuth:     &RuntimeAuthBinding{UsesCodexBackend: true},
	}}
	if mcpResultBodyCaptureAllowed(options, "codex_apps") {
		t.Fatal("capture allowed without an MCP service registration")
	}
	options.MCPResultBodyCapture.Enabled = false
	if mcpResultBodyCaptureAllowed(options, "codex_apps") {
		t.Fatal("capture allowed with the feature disabled")
	}
	if resultBodyRuntimeAuth(nil) != nil {
		t.Fatal("resultBodyRuntimeAuth(nil) != nil")
	}
	binding := resultBodyRuntimeAuth(&RuntimeAuthBinding{UsesCodexBackend: true})
	if binding == nil || !binding.UsesCodexBackend {
		t.Fatalf("resultBodyRuntimeAuth() = %#v", binding)
	}
}
