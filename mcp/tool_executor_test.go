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

// Rust build_mcp_tool_call_request_meta reports the turn-metadata document with
// every MCP call, evaluated per call so live state stays current.
func TestMCPToolExecutorReportsTurnMetadataLikeRust(t *testing.T) {
	documents := 0
	executor := NewToolExecutor(&ToolExecutorOptions{
		ThreadID: "thread-live",
		TurnMetadata: func() map[string]any {
			documents++
			return map[string]any{"thread_id": "thread-live", "turn_id": "turn-1"}
		},
	})
	meta := executor.requestMetaForCall("call-1").(map[string]any)
	document, ok := meta[MCPToolTurnMetadataMetaKey].(map[string]any)
	if !ok || document["turn_id"] != "turn-1" {
		t.Fatalf("turn metadata = %#v (meta %#v)", meta[MCPToolTurnMetadataMetaKey], meta)
	}
	if documents != 1 {
		t.Fatalf("turn metadata callbacks = %d, want 1", documents)
	}
	if _, ok := executor.requestMetaForCall("call-2").(map[string]any); !ok {
		t.Fatal("the second call did not evaluate the turn-metadata provider")
	}
	if documents != 2 {
		t.Fatalf("turn metadata callbacks = %d, want 2", documents)
	}

	// A provider with nothing to report leaves the entry out.
	empty := NewToolExecutor(&ToolExecutorOptions{
		ThreadID:     "thread-live",
		TurnMetadata: func() map[string]any { return nil },
	})
	if meta := empty.requestMetaForCall().(map[string]any); meta[MCPToolTurnMetadataMetaKey] != nil {
		t.Fatalf("empty turn metadata leaked into the call meta: %#v", meta)
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

func TestMCPToolExecutorAttachesConfirmationPoliciesForNodeREPL(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName: "node_repl",
		ConfirmationPolicies: &ActorConfirmationPolicies{
			BrowserUse:  "# Browser confirmations\n\nKeep {{literal_markdown}}.\n",
			ComputerUse: "  # Native confirmations\r\n\nKeep ${native_markdown}.\n",
		},
	})
	meta := executor.requestMetaForCall().(map[string]any)
	policies, ok := meta[ConfirmationPoliciesMetaKey].(map[string]any)
	if !ok {
		t.Fatalf("confirmation policies meta = %#v, want object", meta[ConfirmationPoliciesMetaKey])
	}
	if policies["browser_use"] != "# Browser confirmations\n\nKeep {{literal_markdown}}.\n" ||
		policies["computer_use"] != "  # Native confirmations\r\n\nKeep ${native_markdown}.\n" {
		t.Fatalf("confirmation policies = %#v", policies)
	}
}

func TestMCPToolExecutorConfirmationPoliciesEmptyForNilPolicies(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{ServerName: "cua_repl"})
	meta := executor.requestMetaForCall().(map[string]any)
	policies, ok := meta[ConfirmationPoliciesMetaKey].(map[string]any)
	if !ok || len(policies) != 0 {
		t.Fatalf("confirmation policies meta = %#v, want empty object", meta[ConfirmationPoliciesMetaKey])
	}
}

func TestMCPToolExecutorOmitConfirmationPoliciesForNonNodeREPL(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName:           "filesystem",
		ConfirmationPolicies: &ActorConfirmationPolicies{BrowserUse: "b"},
	})
	meta, _ := executor.requestMetaForCall().(map[string]any)
	if meta != nil {
		if _, ok := meta[ConfirmationPoliciesMetaKey]; ok {
			t.Fatalf("confirmation policies meta present for non-node-repl server: %#v", meta)
		}
	}
}

func TestMCPToolExecutorSuppressConfirmationPoliciesForGuardian(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName:                        "cua_repl",
		SuppressActorConfirmationPolicies: true,
		ConfirmationPolicies:              &ActorConfirmationPolicies{BrowserUse: "b"},
	})
	meta, _ := executor.requestMetaForCall().(map[string]any)
	if meta != nil {
		if _, ok := meta[ConfirmationPoliciesMetaKey]; ok {
			t.Fatalf("confirmation policies meta present for Guardian session: %#v", meta)
		}
	}
}

func TestIsNodeReplBackedServer(t *testing.T) {
	for _, name := range []string{"node_repl", "cua_repl"} {
		if !IsNodeReplBackedServer(name) {
			t.Fatalf("IsNodeReplBackedServer(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"filesystem", "calendar", "mcp__node_repl", "", "node_repl "} {
		if IsNodeReplBackedServer(name) {
			t.Fatalf("IsNodeReplBackedServer(%q) = true, want false", name)
		}
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
	if pre.McpTool == nil {
		t.Fatal("pre.McpTool = nil, want populated MCP provenance")
	}
	if pre.McpTool.ServerName != "filesystem" || pre.McpTool.ToolName != "read_file" {
		t.Fatalf("pre.McpTool = %#v", pre.McpTool)
	}
	if pre.McpTool.Source != tool.McpToolSourceConfig {
		t.Fatalf("pre.McpTool.Source = %q, want config", pre.McpTool.Source)
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
		{Type: "text", Text: "gAAAA-test", Raw: map[string]any{
			"_meta": map[string]any{"codex/encryptedContent": true},
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

// Mirrors Rust convert_mcp_content_to_items (#40737): media and unknown
// content are preserved as typed items instead of dropping or flattening them.
func TestMCPToolModelContentItemsConvertMediaAndUnknownLikeRust(t *testing.T) {
	response := &MCPToolCallResponse{Content: []MCPToolCallContent{
		{Type: "text", Text: "hello"},
		{Type: "image", Raw: map[string]any{
			"type":     "image",
			"data":     "data:image/png;base64,Zm9v",
			"mimeType": "image/png",
		}},
		{Type: "image", Raw: map[string]any{
			"type":     "image",
			"data":     "Zm9v",
			"mimeType": "image/png",
			"_meta":    map[string]any{"codex/imageDetail": "original"},
		}},
		{Type: "audio", Raw: map[string]any{
			"type":     "audio",
			"data":     "Zm9v",
			"mimeType": "audio/wav",
		}},
		{Type: "resource", Raw: map[string]any{
			"type": "resource",
			"uri":  "file:///tmp/x",
		}},
	}}
	got := mcpToolModelContentItems(response)
	want := []any{
		map[string]any{"type": "input_text", "text": "hello"},
		map[string]any{"type": "input_image", "image_url": "data:image/png;base64,Zm9v", "detail": "high"},
		map[string]any{"type": "input_image", "image_url": "data:image/png;base64,Zm9v", "detail": "original"},
		map[string]any{"type": "input_audio", "audio_url": "data:audio/wav;base64,Zm9v"},
		map[string]any{"type": "input_text", "text": `{"type":"resource","uri":"file:///tmp/x"}`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("content items = %#v, want %#v", got, want)
	}
}

func TestMCPFunctionCallOutputPrefersStructuredTextUnlessEncrypted(t *testing.T) {
	// Structured content is serialized to text and does not also become content
	// items (Rust #40737).
	structured := &MCPToolCallResponse{
		Content:           []MCPToolCallContent{{Type: "text", Text: "hello"}},
		StructuredContent: map[string]any{"answer": 42},
	}
	body, items, useItems := mcpFunctionCallOutput(structured)
	if body != `{"answer":42}` || items != nil || useItems {
		t.Fatalf("structured output = %q, %#v, %t", body, items, useItems)
	}

	// A single encrypted content item forces content items even when structured
	// content is present.
	encrypted := &MCPToolCallResponse{
		Content: []MCPToolCallContent{
			{Type: "text", Text: "opaque", Raw: map[string]any{"_meta": map[string]any{"codex/encryptedContent": true}}},
		},
		StructuredContent: map[string]any{"answer": 42},
	}
	_, items, useItems = mcpFunctionCallOutput(encrypted)
	if !useItems || len(items) != 1 {
		t.Fatalf("encrypted output items = %#v, use=%t", items, useItems)
	}
	entry, _ := items[0].(map[string]any)
	if entry["type"] != "encrypted_content" || entry["encrypted_content"] != "opaque" {
		t.Fatalf("encrypted item = %#v", entry)
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

func TestMCPToolContextClassifiesConnectorAndPluginLikeRust(t *testing.T) {
	connectorExecutor := NewToolExecutor(&ToolExecutorOptions{
		ServerName:  "codex-apps",
		ConnectorID: "connector-slack",
		ToolInfo:    &MCPToolInfo{Name: "read"},
	})
	invocation := &tool.Invocation{
		CallID:   "call-connector",
		ToolName: tool.NamespacedName("codex-apps", "read"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
	}
	pre, ok := connectorExecutor.PreToolUsePayload(invocation)
	if !ok || pre.McpTool == nil {
		t.Fatalf("connector pre = %#v (%v)", pre, ok)
	}
	if pre.McpTool.Source != tool.McpToolSourceConnector || pre.McpTool.Connector != "connector-slack" {
		t.Fatalf("connector McpTool = %#v", pre.McpTool)
	}

	pluginExecutor := NewToolExecutor(&ToolExecutorOptions{
		ServerName:  "plugin-server",
		AgentPlugin: true,
		ToolInfo:    &MCPToolInfo{Name: "ping"},
	})
	pre2, ok2 := pluginExecutor.PreToolUsePayload(invocation)
	if !ok2 || pre2.McpTool == nil {
		t.Fatalf("plugin pre = %#v (%v)", pre2, ok2)
	}
	if pre2.McpTool.Source != tool.McpToolSourcePlugin {
		t.Fatalf("plugin McpTool.Source = %q, want plugin", pre2.McpTool.Source)
	}
}

// TestToolExecutorRequestsCodexAppsAuthElicitationLikeRust covers Rust
// maybe_request_codex_apps_auth_elicitation: a Codex Apps auth failure prompts
// the client with the connector URL elicitation and, on acceptance, the model
// receives a completed result after the catalog refresh.
func TestToolExecutorRequestsCodexAppsAuthElicitationLikeRust(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName:    RuntimeCodexAppsMCPServerName,
		ConnectorID:   "connector_calendar",
		ConnectorName: "Google Calendar",
		ThreadID:      "thread-1",
		TurnID:        "turn-1",
	})
	var requests []*MCPElicitationRequest
	refreshed := false
	executor.authElicitation = &AuthElicitationOptions{
		Request: func(_ context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			requests = append(requests, request)
			return &MCPElicitationResponse{Action: MCPElicitationActionAccept}, nil
		},
		RefreshCodexApps: func(context.Context) error {
			refreshed = true
			return nil
		},
		InstallURL: func(name string, connectorID string) string {
			return "https://chatgpt.com/apps/" + name + "/" + connectorID
		},
	}
	result := authFailureResult()
	got := executor.maybeRequestCodexAppsAuthElicitation(context.Background(), "call-1", result)
	if len(requests) != 1 {
		t.Fatalf("elicitation requests = %d, want 1", len(requests))
	}
	request := requests[0]
	if request.ServerName != RuntimeCodexAppsMCPServerName || request.Method != "elicitation/create" {
		t.Fatalf("request identity = %#v", request)
	}
	if request.URL != "https://chatgpt.com/apps/Google Calendar/connector_calendar" {
		t.Fatalf("request url = %q", request.URL)
	}
	if request.ElicitationID != "codex_apps_auth_call-1" || request.ThreadID != "thread-1" || request.TurnID != "turn-1" {
		t.Fatalf("request ids = %#v", request)
	}
	if !refreshed {
		t.Fatal("catalog was not refreshed after acceptance")
	}
	if got == nil || got == result || got.IsError == nil || !*got.IsError {
		t.Fatalf("completed result = %#v", got)
	}
	if !strings.Contains(MCPToolResponseText(got), "Google Calendar") {
		t.Fatalf("completed result text = %q", MCPToolResponseText(got))
	}
	if got.Meta == nil {
		t.Fatal("completed result dropped the original meta")
	}
}

func TestToolExecutorLeavesResultWhenAuthElicitationIsDeclined(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName:    RuntimeCodexAppsMCPServerName,
		ConnectorID:   "connector_calendar",
		ConnectorName: "Google Calendar",
	})
	refreshed := false
	executor.authElicitation = &AuthElicitationOptions{
		Request: func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			return &MCPElicitationResponse{Action: MCPElicitationActionDecline}, nil
		},
		RefreshCodexApps: func(context.Context) error {
			refreshed = true
			return nil
		},
		InstallURL: func(string, string) string { return "https://example.com/install" },
	}
	result := authFailureResult()
	if got := executor.maybeRequestCodexAppsAuthElicitation(context.Background(), "call-1", result); got != result {
		t.Fatalf("declined elicitation changed the result: %#v", got)
	}
	if refreshed {
		t.Fatal("declined elicitation must not refresh the catalog")
	}
}

func TestToolExecutorSkipsAuthElicitationOutsideCodexApps(t *testing.T) {
	executor := NewToolExecutor(&ToolExecutorOptions{
		ServerName:    "memory",
		ConnectorID:   "connector_calendar",
		ConnectorName: "Google Calendar",
	})
	requested := false
	executor.authElicitation = &AuthElicitationOptions{
		Request: func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			requested = true
			return &MCPElicitationResponse{Action: MCPElicitationActionAccept}, nil
		},
		InstallURL: func(string, string) string { return "https://example.com/install" },
	}
	result := authFailureResult()
	if got := executor.maybeRequestCodexAppsAuthElicitation(context.Background(), "call-1", result); got != result {
		t.Fatalf("non-Codex-Apps result changed: %#v", got)
	}
	if requested {
		t.Fatal("non-Codex-Apps server must not request an elicitation")
	}
}
