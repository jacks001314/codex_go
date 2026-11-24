package appserver

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"codex_go/mcp"
	"codex_go/turn"
)

type encryptedMcpHookExecutor struct {
	output HookMcpOutput
}

func (e *encryptedMcpHookExecutor) Execute(context.Context, HookMcpCall) (HookMcpOutput, error) {
	return e.output, nil
}

func encryptedSessionStartHookMetadata(toolName string) HookMetadata {
	return HookMetadata{
		Key:          "mcp-session-start",
		EventName:    HookEventSessionStart,
		HandlerType:  HookHandlerMCPTool,
		Server:       stringPointer("memory"),
		Tool:         stringPointer(toolName),
		TimeoutSec:   5,
		SourcePath:   "/tmp/hooks.json",
		Source:       HookSourcePlugin,
		DisplayOrder: 1,
		Enabled:      true,
		TrustStatus:  HookTrustTrusted,
	}
}

// Rust #63474: a text block marked `codex/encryptedContent` never joins the
// plaintext hook output, so previews and plaintext spill files cannot carry it.
func TestMcpHookResponseSplitsEncryptedContentLikeRust(t *testing.T) {
	response := &mcp.MCPToolCallResponse{Content: []mcp.MCPToolCallContent{
		{Type: "text", Text: "Lookup completed", Raw: map[string]any{"type": "text", "text": "Lookup completed"}},
		{Type: "text", Text: "gAAAA-test", Raw: map[string]any{
			"type":  "text",
			"text":  "gAAAA-test",
			"_meta": map[string]any{"codex/encryptedContent": true},
		}},
	}}
	text, encrypted := mcpHookResponseContentBlocks(response)
	if text != "Lookup completed" {
		t.Fatalf("plaintext = %q", text)
	}
	if !reflect.DeepEqual(encrypted, []string{"gAAAA-test"}) {
		t.Fatalf("encrypted = %#v", encrypted)
	}
}

// Rust #63474: a synchronous SessionStart hook's encrypted block reaches the
// model as an encrypted tool output attributed to the MCP tool.
func TestHookRunnerEncryptedSessionStartContextLikeRust(t *testing.T) {
	runner := NewHookRunner()
	executor := &encryptedMcpHookExecutor{output: HookMcpOutput{Text: "Lookup completed", EncryptedContent: []string{"gAAAA-test"}}}
	runner.McpToolHookExecutor = executor
	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-1",
		CWD:       t.TempDir(),
		EventName: HookEventSessionStart,
		Hooks:     []HookMetadata{encryptedSessionStartHookMetadata("encrypted_output")},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := []turn.HookEncryptedContext{{Name: "encrypted_output", Content: "gAAAA-test"}}
	if !reflect.DeepEqual(result.EncryptedContexts, want) {
		t.Fatalf("encrypted contexts = %#v, want %#v", result.EncryptedContexts, want)
	}
	if !strings.Contains(result.AdditionalContexts[0], "Lookup completed") {
		t.Fatalf("additional contexts = %#v", result.AdditionalContexts)
	}
}

// Rust #63474: an empty encrypted block and one over the 40,000-byte ciphertext
// cap both fail the handler.
func TestHookRunnerRejectsInvalidEncryptedSessionStartContextLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		contents []string
	}{
		{name: "empty block", contents: []string{""}},
		{name: "oversized block", contents: []string{strings.Repeat("a", maxHookEncryptedContextBytes+1)}},
		{name: "oversized run", contents: []string{strings.Repeat("a", 30_000), strings.Repeat("b", 30_000)}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runner := NewHookRunner()
			runner.McpToolHookExecutor = &encryptedMcpHookExecutor{output: HookMcpOutput{EncryptedContent: testCase.contents}}
			result, err := runner.Run(context.Background(), &HookRunRequest{
				ThreadID:  "thread-1",
				CWD:       t.TempDir(),
				EventName: HookEventSessionStart,
				Hooks:     []HookMetadata{encryptedSessionStartHookMetadata("encrypted_output")},
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(result.EncryptedContexts) != 0 {
				t.Fatalf("encrypted contexts = %#v", result.EncryptedContexts)
			}
			if len(result.Runs) != 1 || result.Runs[0].Status == HookRunCompleted {
				t.Fatalf("runs = %#v", result.Runs)
			}
		})
	}
}

// Rust #63474: the model request carries the ciphertext as a standalone
// function_call_output item whose payload is an encrypted_content content item.
func TestHookEncryptedContextInputItemsLikeRust(t *testing.T) {
	items := hookEncryptedContextInputItems([]turn.HookEncryptedContext{
		{Name: "encrypted_output", Namespace: "mcp__memory", Content: "gAAAA-test"},
		{Name: "ignored", Content: ""},
	})
	if len(items) != 1 {
		t.Fatalf("items = %#v", items)
	}
	item := items[0].(map[string]any)
	if item["type"] != "function_call_output" || item["name"] != "encrypted_output" || item["namespace"] != "mcp__memory" {
		t.Fatalf("item = %#v", item)
	}
	output := item["output"].([]map[string]any)
	if len(output) != 1 || output[0]["type"] != "encrypted_content" || output[0]["encrypted_content"] != "gAAAA-test" {
		t.Fatalf("output = %#v", output)
	}
}
