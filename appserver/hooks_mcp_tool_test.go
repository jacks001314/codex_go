package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type recordingMcpToolHookExecutor struct {
	server  string
	tool    string
	input   map[string]any
	output  string
	err     error
	timeout time.Duration
	called  bool
}

func (e *recordingMcpToolHookExecutor) Execute(ctx context.Context, call HookMcpCall) (string, error) {
	e.called = true
	e.server = call.Server
	e.tool = call.Tool
	e.input = call.Input
	e.timeout = call.Timeout
	return e.output, e.err
}

func TestExpandMcpArgumentTemplateMatchesRust(t *testing.T) {
	hookEvent := map[string]any{
		"tool_input": map[string]any{
			"count": float64(3),
			"id":    "ENG-1",
			"ok":    true,
		},
	}
	input, err := expandMcpArgumentTemplate(map[string]any{
		"count": "${tool_input.count}",
		"label": "issue-${tool_input.id}",
		"ok":    "${tool_input.ok}",
		"nested": map[string]any{
			"id": "${tool_input.id}",
		},
		"array":  []any{"${tool_input.id}", "static"},
		"static": "keep",
	}, hookEvent)
	if err != nil {
		t.Fatalf("expandMcpArgumentTemplate() error = %v", err)
	}
	if input["count"] != float64(3) {
		t.Fatalf("count = %#v, want preserved JSON number 3", input["count"])
	}
	if input["label"] != "issue-ENG-1" {
		t.Fatalf("label = %#v, want embedded placeholder rendered as string", input["label"])
	}
	if input["ok"] != true {
		t.Fatalf("ok = %#v, want preserved boolean", input["ok"])
	}
	nested, ok := input["nested"].(map[string]any)
	if !ok || nested["id"] != "ENG-1" {
		t.Fatalf("nested = %#v", input["nested"])
	}
	array, ok := input["array"].([]any)
	if !ok || len(array) != 2 || array[0] != "ENG-1" || array[1] != "static" {
		t.Fatalf("array = %#v", input["array"])
	}
	if input["static"] != "keep" {
		t.Fatalf("static = %#v", input["static"])
	}

	if _, err := expandMcpArgumentTemplate(map[string]any{
		"id": "${tool_input.missing}",
	}, hookEvent); err == nil {
		t.Fatal("missing field should fail the hook like Rust")
	}
}

func TestHookRunnerRunsMcpToolHookLikeRust(t *testing.T) {
	runner := NewHookRunner()
	server := "linear"
	toolName := "get_issue"
	executor := &recordingMcpToolHookExecutor{
		output: `{"decision":"block","reason":"high risk issue"}`,
	}
	runner.McpToolHookExecutor = executor
	hook := HookMetadata{
		Key:          "mcp-pre",
		EventName:    HookEventPreToolUse,
		HandlerType:  HookHandlerMCPTool,
		Server:       &server,
		Tool:         &toolName,
		Input:        map[string]any{"issue_id": "${tool_input.id}"},
		TimeoutSec:   5,
		SourcePath:   "/tmp/hooks.json",
		Source:       HookSourceUser,
		DisplayOrder: 1,
		Enabled:      true,
		TrustStatus:  HookTrustTrusted,
	}

	result, err := runner.RunPreToolUse(context.Background(), &HookPreToolUseRequest{
		ThreadID:       "thread-1",
		TurnID:         "turn-1",
		CWD:            t.TempDir(),
		Model:          "gpt-test",
		PermissionMode: "default",
		ToolName:       "linear.get_issue",
		ToolUseID:      "call-1",
		ToolInput:      map[string]any{"id": "ENG-9"},
		Hooks:          []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("RunPreToolUse() error = %v", err)
	}
	if !executor.called || executor.server != "linear" || executor.tool != "get_issue" {
		t.Fatalf("executor call = server=%q tool=%q called=%v", executor.server, executor.tool, executor.called)
	}
	if executor.input["issue_id"] != "ENG-9" {
		t.Fatalf("executor input = %#v, want expanded issue_id", executor.input)
	}
	if executor.timeout != 5*time.Second {
		t.Fatalf("executor timeout = %v, want 5s", executor.timeout)
	}
	if !result.Blocked || result.BlockReason != "high risk issue" {
		t.Fatalf("result = %+v, want blocked by MCP tool output", result)
	}
	if len(result.Runs) != 1 || result.Runs[0].HandlerType != HookHandlerMCPTool || result.Runs[0].Status != HookRunBlocked {
		t.Fatalf("runs = %+v", result.Runs)
	}
}

func TestHookRunnerMcpToolUpdatedInputUsesCommandHookSemantics(t *testing.T) {
	runner := NewHookRunner()
	server := "linear"
	toolName := "get_issue"
	runner.McpToolHookExecutor = &recordingMcpToolHookExecutor{
		output: `{"hookSpecificOutput":{"permissionDecision":"allow","updatedInput":{"command":"echo adjusted"}}}`,
	}
	hook := HookMetadata{
		Key:          "mcp-pre",
		EventName:    HookEventPreToolUse,
		HandlerType:  HookHandlerMCPTool,
		Server:       &server,
		Tool:         &toolName,
		Input:        map[string]any{"issue_id": "${tool_input.id}"},
		TimeoutSec:   5,
		SourcePath:   "/tmp/hooks.json",
		Source:       HookSourceUser,
		DisplayOrder: 1,
		Enabled:      true,
		TrustStatus:  HookTrustTrusted,
	}
	result, err := runner.RunPreToolUse(context.Background(), &HookPreToolUseRequest{
		ThreadID:  "thread-1",
		CWD:       t.TempDir(),
		ToolName:  "linear.get_issue",
		ToolUseID: "call-2",
		ToolInput: map[string]any{"id": "ENG-9"},
		Hooks:     []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("RunPreToolUse() error = %v", err)
	}
	if updated, ok := result.UpdatedInput.(map[string]any); !ok || updated["command"] != "echo adjusted" {
		t.Fatalf("updatedInput = %#v", result.UpdatedInput)
	}
	if result.Blocked {
		t.Fatalf("result = %+v, want allowed with updated input", result)
	}
}

func TestHookRunnerMcpToolWithoutExecutorFailsClosed(t *testing.T) {
	runner := NewHookRunner()
	server := "linear"
	toolName := "get_issue"
	hook := HookMetadata{
		Key:          "mcp-pre",
		EventName:    HookEventPreToolUse,
		HandlerType:  HookHandlerMCPTool,
		Server:       &server,
		Tool:         &toolName,
		TimeoutSec:   5,
		SourcePath:   "/tmp/hooks.json",
		Source:       HookSourceUser,
		DisplayOrder: 1,
		Enabled:      true,
		TrustStatus:  HookTrustTrusted,
	}
	result, err := runner.RunPreToolUse(context.Background(), &HookPreToolUseRequest{
		ThreadID: "thread-1",
		CWD:      t.TempDir(),
		ToolName: "linear.get_issue",
		Hooks:    []HookMetadata{hook},
	})
	if err != nil {
		t.Fatalf("RunPreToolUse() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunFailed {
		t.Fatalf("runs = %+v, want failed run", result.Runs)
	}
	joined := jsonEncodeHookRunForTest(result)
	if !strings.Contains(joined, "MCP invocation is not available yet") {
		t.Fatalf("failed run output = %s, want MCP invocation unavailable error", joined)
	}
}

func jsonEncodeHookRunForTest(result *HookRunResult) string {
	data, err := json.Marshal(result)
	if err != nil {
		return ""
	}
	return string(data)
}
