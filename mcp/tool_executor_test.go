package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"codex_go/tool"
)

// TestAgentPluginOversizedSchemaDegradesToAcceptAnything mirrors Rust
// agent_plugin_mcp_tool_to_responses_api_tool: Agent Plugin v1 tools whose
// normalized schema still exceeds MAX_SERIALIZED_MCP_TOOL_BYTES degrade to
// {"type":"object","additionalProperties":true}; regular MCP tools keep their
// (compacted) schema.
func TestAgentPluginOversizedSchemaDegradesToAcceptAnything(t *testing.T) {
	enum := make([]any, 0, 4000)
	for i := 0; i < 4000; i++ {
		enum = append(enum, "value-"+strconv.Itoa(i))
	}
	hugeSchema := map[string]any{
		"type":        "object",
		"description": "A schema that compaction cannot shrink below the cap.",
		"properties": map[string]any{
			"choice": map[string]any{"type": "string", "enum": enum},
		},
	}

	agentPlugin := NewToolExecutor(&ToolExecutorOptions{
		ServerName:  "agent-plugin-server",
		AgentPlugin: true,
		ToolInfo: &MCPToolInfo{
			Name:        "create",
			Description: "Create",
			InputSchema: hugeSchema,
		},
	})
	got := agentPlugin.Spec().InputSchema
	want := map[string]any{"type": "object", "additionalProperties": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agent-plugin Spec().InputSchema = %#v, want %#v", got, want)
	}

	regular := NewToolExecutor(&ToolExecutorOptions{
		ServerName: "regular-server",
		ToolInfo: &MCPToolInfo{
			Name:        "create",
			Description: "Create",
			InputSchema: hugeSchema,
		},
	})
	gotRegular := regular.Spec().InputSchema
	if reflect.DeepEqual(gotRegular, want) {
		t.Fatalf("regular MCP Spec().InputSchema degraded unexpectedly: %#v", gotRegular)
	}
	if _, ok := gotRegular["properties"].(map[string]any); !ok {
		t.Fatalf("regular MCP Spec().InputSchema = %#v, want preserved properties", gotRegular)
	}
}

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
	if output.Data["server"] != "memory" || output.Data["tool"] != "create_entities" {
		t.Fatalf("raw MCP identity = %#v", output.Data)
	}
	spec := executor.Spec()
	if spec.Name.Key() != "memory.create_entities" || spec.Description != "Create memory entities" || spec.InputSchema["type"] != "object" {
		t.Fatalf("Spec = %#v", spec)
	}
	if spec.Search == nil || spec.Search.Source == nil || spec.Search.Source.Name != "memory" || !strings.Contains(spec.Search.Text, "create_entities") {
		t.Fatalf("Search = %#v", spec.Search)
	}
}

func TestMCPToolExecutorTelemetryTagsAreSynchronousLikeRust(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName:   "calendar",
		ServerOrigin: "https://mcp.example.test",
		ToolInfo:     &MCPToolInfo{Name: "events"},
	})
	tags := executor.TelemetryTags(&tool.Invocation{})
	if tags["mcp_server"] != "calendar" || tags["mcp_server_origin"] != "https://mcp.example.test" || len(tags) != 2 {
		t.Fatalf("TelemetryTags() = %#v", tags)
	}
}

func TestMCPToolExecutorRequestMetaIncludesThreadIDLikeRust(t *testing.T) {
	static := map[string]any{"plugin_id": "sample@test"}
	executor := NewToolExecutor(&ToolExecutorOptions{ThreadID: "thread-live", RequestMeta: static})
	meta, ok := executor.requestMetaForCall().(map[string]any)
	if !ok || meta["thread_id"] != "thread-live" || meta["plugin_id"] != "sample@test" {
		t.Fatalf("request meta = %#v", meta)
	}
	meta["plugin_id"] = "changed"
	if static["plugin_id"] != "sample@test" {
		t.Fatalf("input request meta was mutated: %#v", static)
	}
}

func TestCodexAppsMCPToolRequestMetaIncludesCallIDLikeRust(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{ServerName: RuntimeCodexAppsMCPServerName, ThreadID: "thread-live", RequestMeta: map[string]any{"_codex_apps": map[string]any{"connector_id": "calendar"}}})
	meta := executor.requestMetaForCall("call-123").(map[string]any)
	apps, ok := meta["_codex_apps"].(map[string]any)
	if !ok || apps["call_id"] != "call-123" || apps["connector_id"] != "calendar" || meta["thread_id"] != "thread-live" {
		t.Fatalf("request meta = %#v", meta)
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

func TestCodexAppsMCPToolExecutorUploadsDeclaredFilesAfterPreHook(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	executor := NewToolExecutor(&ToolExecutorOptions{
		Service:    NewMCPService(nil),
		ServerName: RuntimeCodexAppsMCPServerName,
		ToolInfo:   &MCPToolInfo{Name: "capture"},
		OpenAIFileRewriter: NewOpenAIFileRewriter(
			dir,
			&OpenAIFileAuth{ChatGPTBackend: true},
			&fakeUploader{},
		),
		OpenAIFileInputOptionalFields: map[string][]string{"file": {"file_name"}},
	})
	invocation := &tool.Invocation{
		CallID:   "call-upload",
		ToolName: tool.NamespacedName("mcp__codex_apps", "capture"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"file":"notes.txt"}`},
	}
	pre, ok := executor.PreToolUsePayload(invocation)
	if !ok || pre.ToolInput.(map[string]any)["file"] != "notes.txt" {
		t.Fatalf("pre payload = %#v ok=%v", pre, ok)
	}
	output, err := executor.Execute(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(output.Body, `"file_id":"file_123"`) || !strings.Contains(output.Body, `"file_name":"notes.txt"`) {
		t.Fatalf("MCP body did not receive rewritten arguments: %s", output.Body)
	}
	post, ok := executor.PostToolUsePayload(invocation, output)
	if !ok {
		t.Fatal("PostToolUsePayload() ok = false")
	}
	file, ok := post.ToolInput.(map[string]any)["file"].(map[string]any)
	if !ok || file["file_id"] != "file_123" || file["file_name"] != "notes.txt" {
		t.Fatalf("post tool input = %#v", post.ToolInput)
	}
}

func TestCodexAppsMCPToolExecutorHostedFileUploadContextLikeRust(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName:  RuntimeCodexAppsMCPServerName,
		ConnectorID: "library",
		Model:       "gpt-work",
		ToolInfo: &MCPToolInfo{Name: "create_library_file", Meta: map[string]any{
			"_codex_apps": map[string]any{"resource_uri": "sediment://apps/library/create_library_file"},
		}},
	})
	got := executor.hostedFileUploadContext()
	if got == nil || got.ConnectorID != "library" || got.ActionName != "create_library_file" || got.Model != "gpt-work" {
		t.Fatalf("hostedFileUploadContext() = %#v", got)
	}

	other := NewToolExecutor(&ToolExecutorOptions{ServerName: "calendar", ConnectorID: "library", Model: "gpt-work"})
	if other.hostedFileUploadContext() != nil {
		t.Fatal("non-Codex-Apps server should have no hosted context")
	}

	missingAction := NewToolExecutor(&ToolExecutorOptions{
		ServerName:  RuntimeCodexAppsMCPServerName,
		ConnectorID: "library",
		Model:       "gpt-work",
		ToolInfo:    &MCPToolInfo{Name: "capture"},
	})
	if missingAction.hostedFileUploadContext() != nil {
		t.Fatal("missing action name should have no hosted context")
	}

	missingModel := NewToolExecutor(&ToolExecutorOptions{
		ServerName:  RuntimeCodexAppsMCPServerName,
		ConnectorID: "library",
		ToolInfo: &MCPToolInfo{Name: "capture", Meta: map[string]any{
			"_codex_apps": map[string]any{"resource_uri": "sediment://apps/library/capture"},
		}},
	})
	if missingModel.hostedFileUploadContext() != nil {
		t.Fatal("missing model should have no hosted context")
	}
}

func TestMcpToolCallActionNameFromCodexAppsMetaLikeRust(t *testing.T) {
	cases := []struct {
		name string
		meta any
		want string
	}{
		{"underscore key", map[string]any{"_codex_apps": map[string]any{"resource_uri": "sediment://apps/library/create_library_file"}}, "create_library_file"},
		{"trailing slash", map[string]any{"codex_apps": map[string]any{"resource_uri": "sediment://apps/library/create_library_file/"}}, "create_library_file"},
		{"camel key", map[string]any{"codexApps": map[string]any{"resource_uri": "/apps/library/do_thing"}}, "do_thing"},
		{"missing resource uri", map[string]any{"_codex_apps": map[string]any{}}, ""},
		{"empty after trim", map[string]any{"_codex_apps": map[string]any{"resource_uri": "/"}}, ""},
		{"nil meta", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mcpToolCallActionName(tc.meta); got != tc.want {
				t.Fatalf("mcpToolCallActionName() = %q, want %q", got, tc.want)
			}
		})
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
	if _, ok := registry.Lookup(tool.NamespacedName("mcp__server", "read")); !ok {
		t.Fatal("MCP tool not registered")
	}

	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName: "server",
		ToolInfo:   &MCPToolInfo{Name: "read", Annotations: map[string]any{"readOnlyHint": readOnly}},
	})
	if !executor.Spec().Parallel {
		t.Fatalf("read-only MCP tool should support parallel calls")
	}
	if executor.Spec().ReadOnlyHint == nil || !*executor.Spec().ReadOnlyHint {
		t.Fatalf("read-only MCP tool hint = %#v", executor.Spec().ReadOnlyHint)
	}
	writeCapable := NewToolExecutor(&ToolExecutorOptions{
		ServerName: "server",
		ToolInfo:   &MCPToolInfo{Name: "write", Annotations: map[string]any{"readOnlyHint": false}},
	})
	if writeCapable.Spec().ReadOnlyHint == nil || *writeCapable.Spec().ReadOnlyHint || writeCapable.Spec().Parallel {
		t.Fatalf("write-capable MCP spec = %#v", writeCapable.Spec())
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

func TestMCPToolModelContentItemsPreserveEncryptedContentLikeRust(t *testing.T) {
	response := &MCPToolCallResponse{Content: []MCPToolCallContent{
		{Type: "text", Text: "Lookup completed"},
		{Type: "encrypted_content", Raw: map[string]any{
			"type":              "encrypted_content",
			"encrypted_content": "gAAAA-test",
		}},
	}}
	got := mcpToolModelContentItems(response)
	want := []any{
		map[string]any{"type": "input_text", "text": "Lookup completed"},
		map[string]any{"type": "encrypted_content", "encrypted_content": "gAAAA-test"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("content items = %#v, want %#v", got, want)
	}
}

func TestMcpToolCallAppContextOmitsRemovedTemplateIDLikeRust(t *testing.T) {
	encoded, err := json.Marshal(McpToolCallAppContext{ConnectorID: "connector"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), "templateId") {
		t.Fatalf("app context = %s", encoded)
	}
}
