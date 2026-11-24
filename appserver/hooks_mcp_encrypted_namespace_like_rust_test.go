package appserver

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/mcp"
	"codex_go/turn"
)

// Rust #63474: ciphertext from a synchronous SessionStart hook keeps the
// callable namespace of the tool that produced it, so the model request and the
// replayed session item both attribute the encrypted output to that namespace.
func TestHookRunnerEncryptedContextKeepsCallableNamespaceLikeRust(t *testing.T) {
	runner := NewHookRunner()
	runner.McpToolHookExecutor = &encryptedMcpHookExecutor{output: HookMcpOutput{
		Text:                "Lookup completed",
		EncryptedContent:    []string{"gAAAA-test"},
		SourceToolNamespace: "memory",
	}}
	result, err := runner.Run(context.Background(), &HookRunRequest{
		ThreadID:  "thread-1",
		CWD:       t.TempDir(),
		EventName: HookEventSessionStart,
		Hooks:     []HookMetadata{encryptedSessionStartHookMetadata("encrypted_output")},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := []turn.HookEncryptedContext{{Name: "encrypted_output", Namespace: "memory", Content: "gAAAA-test"}}
	if !reflect.DeepEqual(result.EncryptedContexts, want) {
		t.Fatalf("encrypted contexts = %#v, want %#v", result.EncryptedContexts, want)
	}

	items := hookEncryptedContextInputItems(result.EncryptedContexts)
	if len(items) != 1 {
		t.Fatalf("input items = %#v", items)
	}
	item := items[0].(map[string]any)
	if item["namespace"] != "memory" {
		t.Fatalf("input item namespace = %#v", item["namespace"])
	}

	sessionItems := hookEncryptedContextSessionItems("turn-1", result.EncryptedContexts, time.Unix(0, 0).UTC())
	if len(sessionItems) != 1 {
		t.Fatalf("session items = %#v", sessionItems)
	}
	if sessionItems[0].Namespace != "memory" {
		t.Fatalf("session item namespace = %q", sessionItems[0].Namespace)
	}
	if !strings.Contains(string(sessionItems[0].Raw), `"namespace":"memory"`) {
		t.Fatalf("replayed payload lost the namespace: %s", sessionItems[0].Raw)
	}
}

// Rust #63474 source_tool_namespace: the namespace comes from the runtime's
// tool catalog, and an unknown tool stays unattributed instead of borrowing
// another tool's namespace.
func TestHookEncryptedToolNamespaceLikeRust(t *testing.T) {
	lookup := func(server string, tool string) *mcp.RuntimeToolInfo {
		if server == "memory" && tool == "encrypted_output" {
			return &mcp.RuntimeToolInfo{ServerName: "memory", CallableName: "encrypted_output", CallableNamespace: "mcp__memory"}
		}
		return nil
	}
	if got := hookEncryptedToolNamespace(lookup, "memory", "encrypted_output"); got != "mcp__memory" {
		t.Fatalf("known tool namespace = %q", got)
	}
	if got := hookEncryptedToolNamespace(lookup, "memory", "unknown"); got != "" {
		t.Fatalf("unknown tool namespace = %q, want empty", got)
	}
	if got := hookEncryptedToolNamespace(nil, "memory", "encrypted_output"); got != "" {
		t.Fatalf("missing catalog namespace = %q, want empty", got)
	}
}
