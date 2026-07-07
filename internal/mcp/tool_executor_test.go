package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"codex_go/internal/tool"
)

func TestMCPToolExecutorRunsCall(t *testing.T) {
	service := NewMCPService(nil)
	executor := NewToolExecutor(&ToolExecutorOptions{
		Service:    service,
		ServerName: "memory",
		ToolInfo: &MCPToolInfo{
			Name:        "create_entities",
			Description: "Create memory entities",
			InputSchema: map[string]any{"type": "object"},
		},
	})

	output, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-mcp",
		ToolName: tool.NamespacedName("memory", "create_entities"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"entities":[{"name":"Ada"}]}`},
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !output.Success || !strings.Contains(output.Body, "Ada") {
		t.Fatalf("output = %#v", output)
	}
	if output.Data["hook_response"] == nil {
		t.Fatalf("Data = %#v", output.Data)
	}
	spec := executor.Spec()
	if spec.Name.Key() != "memory.create_entities" || spec.Description != "Create memory entities" || spec.InputSchema["type"] != "object" {
		t.Fatalf("Spec = %#v", spec)
	}
	if spec.Search == nil || spec.Search.Source == nil || spec.Search.Source.Name != "memory" || !strings.Contains(spec.Search.Text, "create_entities") {
		t.Fatalf("Search = %#v", spec.Search)
	}
}

func TestMCPToolExecutorHookPayloadsUsePrefixedName(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName: "filesystem",
		ToolInfo:   &MCPToolInfo{Name: "read_file"},
	})
	invocation := &tool.Invocation{
		CallID:   "call-mcp",
		ToolName: tool.NamespacedName("filesystem", "read_file"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"path":"/tmp/notes.txt"}`},
	}

	pre, ok := executor.PreToolUsePayload(invocation)
	if !ok {
		t.Fatal("PreToolUsePayload() ok = false")
	}
	if pre.ToolName == nil || pre.ToolName.Name != "mcp__filesystem__read_file" {
		t.Fatalf("pre.ToolName = %#v", pre.ToolName)
	}
	if input, ok := pre.ToolInput.(map[string]any); !ok || input["path"] != "/tmp/notes.txt" {
		t.Fatalf("pre.ToolInput = %#v", pre.ToolInput)
	}

	output := &tool.Output{
		CallID: "call-mcp",
		Body:   "notes",
		Data: map[string]any{
			"hook_response": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "notes"}},
			},
		},
	}
	post, ok := executor.PostToolUsePayload(invocation, output)
	if !ok {
		t.Fatal("PostToolUsePayload() ok = false")
	}
	if post.ToolName == nil || post.ToolName.Name != "mcp__filesystem__read_file" || post.ToolUseID != "call-mcp" {
		t.Fatalf("post = %#v", post)
	}
	if input, ok := post.ToolInput.(map[string]any); !ok || input["path"] != "/tmp/notes.txt" {
		t.Fatalf("post.ToolInput = %#v", post.ToolInput)
	}
	response, ok := post.ToolResponse.(map[string]any)
	if !ok || response["content"] == nil {
		t.Fatalf("ToolResponse = %#v", post.ToolResponse)
	}
}

func TestMCPToolExecutorKeepsBuiltinLikeNamesMCPPrefixed(t *testing.T) {
	name := EnsureMCPHookToolName(JoinToolName(tool.NamespacedName("mcp__foo", "exec_command")))
	if name != "mcp__foo__exec_command" {
		t.Fatalf("hook name = %q", name)
	}
}

func TestMCPToolExecutorUpdatedHookInputRewritesArguments(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName: "foo",
		ToolInfo:   &MCPToolInfo{Name: "bar"},
	})
	updated, err := executor.WithUpdatedHookInput(&tool.Invocation{
		ToolName: tool.NamespacedName("foo", "bar"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message":"old"}`},
	}, map[string]any{"message": "new"})
	if err != nil {
		t.Fatalf("WithUpdatedHookInput() error = %v", err)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(updated.Payload.Arguments), &args); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if args["message"] != "new" {
		t.Fatalf("args = %#v", args)
	}
}

func TestMCPRegisterToolExecutors(t *testing.T) {
	readOnly := true
	registry := tool.NewRegistry()
	err := RegisterToolExecutors(registry, NewMCPService(nil), []RuntimeToolInfo{{
		ServerName: "server",
		Tool: RuntimeTool{
			Name: "read",
			Annotations: &RuntimeToolAnnotations{
				DestructiveHint: nil,
				OpenWorldHint:   nil,
			},
		},
	}})
	if err != nil {
		t.Fatalf("RegisterToolExecutors() error = %v", err)
	}
	if _, ok := registry.Lookup(tool.NamespacedName("server", "read")); !ok {
		t.Fatal("MCP tool not registered")
	}

	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName: "server",
		ToolInfo:   &MCPToolInfo{Name: "read", Annotations: map[string]any{"readOnlyHint": readOnly}},
	})
	if !executor.Spec().Parallel {
		t.Fatalf("read-only MCP tool should support parallel calls")
	}
}

func TestMCPToolResponseDataShape(t *testing.T) {
	falseValue := false
	response := &MCPToolCallResponse{
		Content:           []MCPToolCallContent{{Type: "text", Text: "notes"}},
		StructuredContent: map[string]any{"bytes": float64(5)},
		IsError:           &falseValue,
	}
	data := mcpToolResponseData(response)
	if data["isError"] != false {
		t.Fatalf("isError should preserve explicit false: %#v", data)
	}
	hookResponse, ok := data["hook_response"].(map[string]any)
	if !ok {
		t.Fatalf("hook_response = %#v", data["hook_response"])
	}
	if !reflect.DeepEqual(hookResponse["structuredContent"], map[string]any{"bytes": float64(5)}) {
		t.Fatalf("hook_response = %#v", hookResponse)
	}
	if _, ok := hookResponse["hook_response"]; ok {
		t.Fatalf("nested hook_response = %#v", hookResponse)
	}
}
