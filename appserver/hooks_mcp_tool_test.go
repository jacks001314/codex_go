package appserver

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type recordingMcpToolHookExecutor struct {
	server                 string
	tool                   string
	skipDormant            bool
	input                  map[string]any
	envID                  string
	meta                   map[string]any
	expectedAppConnectorID string
	output                 string
	structured             any
	err                    error
	timeout                time.Duration
	called                 bool
}

func (e *recordingMcpToolHookExecutor) Execute(ctx context.Context, call HookMcpCall) (HookMcpOutput, error) {
	e.called = true
	e.server = call.Server
	e.tool = call.Tool
	e.skipDormant = call.SkipDormantReplCleanup
	e.input = call.Input
	e.envID = call.EnvironmentID
	e.meta = call.Metadata
	e.expectedAppConnectorID = call.ExpectedAppConnectorID
	e.timeout = call.Timeout
	return HookMcpOutput{Text: e.output, StructuredContent: e.structured}, e.err
}

func TestHookRunnerStopMcpCarriesRequestMetadataAndEnvironmentLikeRust(t *testing.T) {
	runner := NewHookRunner()
	server := "security"
	toolName := "check"
	environmentID := "executor-a"
	executor := &recordingMcpToolHookExecutor{output: `{}`}
	runner.McpToolHookExecutor = executor
	requestMetadata := map[string]any{
		"x-codex-turn-metadata": map[string]any{"turn_id": "turn-1"},
	}
	result, err := runner.RunStop(context.Background(), &HookStopRequest{
		ThreadID:        "thread-1",
		TurnID:          "turn-1",
		CWD:             t.TempDir(),
		Model:           "gpt-test",
		PermissionMode:  "default",
		RequestMetadata: requestMetadata,
		Hooks: []HookMetadata{{
			Key:           "mcp-stop",
			EventName:     HookEventStop,
			HandlerType:   HookHandlerMCPTool,
			Server:        &server,
			Tool:          &toolName,
			EnvironmentID: &environmentID,
			TimeoutSec:    5,
			SourcePath:    "/tmp/executor-hooks.json",
			Source:        HookSourcePlugin,
			DisplayOrder:  1,
			Enabled:       true,
			TrustStatus:   HookTrustTrusted,
		}},
	})
	if err != nil {
		t.Fatalf("RunStop() error = %v", err)
	}
	if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
		t.Fatalf("runs = %#v", result.Runs)
	}
	if executor.envID != "executor-a" {
		t.Fatalf("environment ID = %q", executor.envID)
	}
	if !reflect.DeepEqual(executor.meta, requestMetadata) {
		t.Fatalf("metadata = %#v, want %#v", executor.meta, requestMetadata)
	}
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

// TestBundledReplCleanupFlagLikeRust mirrors Rust #61437: only a builtin
// node_repl/cua_repl turn_ended handler may skip dormant cleanup startup.
func TestBundledReplCleanupFlagLikeRust(t *testing.T) {
	runner := NewHookRunner()
	executor := &recordingMcpToolHookExecutor{output: `{}`}
	runner.McpToolHookExecutor = executor
	cases := []struct {
		builtin bool
		server  string
		tool    string
		want    bool
	}{
		{builtin: true, server: "node_repl", tool: "turn_ended", want: true},
		{builtin: true, server: "cua_repl", tool: "turn_ended", want: true},
		{builtin: false, server: "node_repl", tool: "turn_ended", want: false},
		{builtin: true, server: "security", tool: "turn_ended", want: false},
		{builtin: true, server: "node_repl", tool: "cleanup", want: false},
	}
	for _, testCase := range cases {
		server, toolName := testCase.server, testCase.tool
		executor.skipDormant = false
		result, err := runner.RunStop(context.Background(), &HookStopRequest{
			ThreadID: "thread-1",
			TurnID:   "turn-1",
			CWD:      t.TempDir(),
			Hooks: []HookMetadata{{
				Key:         "mcp-stop",
				EventName:   HookEventStop,
				HandlerType: HookHandlerMCPTool,
				Server:      &server,
				Tool:        &toolName,
				TimeoutSec:  5,
				Source:      HookSourcePlugin,
				Enabled:     true,
				TrustStatus: HookTrustTrusted,
				Builtin:     testCase.builtin,
			}},
		})
		if err != nil {
			t.Fatalf("RunStop(%s/%s) error = %v", testCase.server, testCase.tool, err)
		}
		if len(result.Runs) != 1 || result.Runs[0].Status != HookRunCompleted {
			t.Fatalf("RunStop(%s/%s) runs = %+v", testCase.server, testCase.tool, result.Runs)
		}
		if executor.skipDormant != testCase.want {
			t.Fatalf("RunStop(%s/%s) skip dormant = %v, want %v", testCase.server, testCase.tool, executor.skipDormant, testCase.want)
		}
	}
}
