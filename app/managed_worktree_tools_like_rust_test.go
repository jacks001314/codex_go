package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/appserver"
)

func TestManagedWorktreeDynamicToolSpecsLikeRust(t *testing.T) {
	specs := ManagedWorktreeDynamicToolSpecs()
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
		if spec.Type != "function" || !spec.DeferLoading {
			t.Fatalf("spec %q = %#v, want a deferred function", spec.Name, spec)
		}
		schema, ok := spec.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("spec %q schema = %#v", spec.Name, spec.InputSchema)
		}
		if schema["type"] != "object" || schema["additionalProperties"] != false {
			t.Fatalf("spec %q schema = %#v, want a strict object", spec.Name, schema)
		}
	}
	if want := []string{"create_worktree", "get_worktree_creation_status", "list_worktrees"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("tool names = %v, want %v", names, want)
	}

	create := specs[0].InputSchema.(map[string]any)
	if !reflect.DeepEqual(create["required"], []string{"allowAsync"}) {
		t.Fatalf("create required = %#v, want allowAsync", create["required"])
	}
	properties := create["properties"].(map[string]any)
	allowAsync := properties["allowAsync"].(map[string]any)
	if allowAsync["type"] != "boolean" || allowAsync["const"] != true {
		t.Fatalf("allowAsync schema = %#v", allowAsync)
	}
	ref := properties["ref"].(map[string]any)
	if ref["maxLength"] != managedWorktreeMaxRevisionBytes {
		t.Fatalf("ref maxLength = %#v, want %d", ref["maxLength"], managedWorktreeMaxRevisionBytes)
	}
}

func TestTaskToolsMCPHostsManagedWorktreeToolsLikeRust(t *testing.T) {
	service := &managedWorktreeTools{now: time.Now, operations: map[string]*managedWorktreeOperation{}}
	server, err := startTaskToolsMCPServer(&taskToolsMCPConnection{}, service)
	if err != nil {
		t.Fatalf("startTaskToolsMCPServer: %v", err)
	}
	defer server.close()
	config := server.configValues()["mcp_servers."+DynamicToolNamespace].(map[string]any)
	toolsConfig := config["tools"].(map[string]any)
	createConfig := toolsConfig["create_worktree"].(map[string]any)
	if approval := createConfig["approval_mode"]; approval != "approve" {
		t.Fatalf("create_worktree approval_mode = %#v, want approve", approval)
	}

	listed := taskToolsMCPPost(t, config, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	tools := map[string]map[string]any{}
	for _, raw := range listed["result"].(map[string]any)["tools"].([]any) {
		tool := raw.(map[string]any)
		tools[tool["name"].(string)] = tool
	}
	for name, readOnly := range map[string]bool{
		"create_worktree":              false,
		"get_worktree_creation_status": true,
		"list_worktrees":               true,
	} {
		tool, ok := tools[name]
		if !ok {
			t.Fatalf("tools/list is missing %q", name)
		}
		annotations := tool["annotations"].(map[string]any)
		if got := annotations["readOnlyHint"]; got != readOnly {
			t.Fatalf("%s readOnlyHint = %#v, want %v", name, got, readOnly)
		}
	}

	response := taskToolsMCPPost(t, config, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "get_worktree_creation_status",
			"arguments": map[string]any{"operationId": "missing"},
			"_meta":     map[string]any{"threadId": "thread-a"},
		},
	})
	result := response["result"].(map[string]any)
	if isError, _ := result["isError"].(bool); !isError {
		t.Fatalf("unknown operation result = %#v, want isError", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, managedWorktreeUnknownOperationMessage) {
		t.Fatalf("unknown operation text = %q", text)
	}
}

func TestTaskToolsMCPRejectsWorktreeCallsWithoutServiceLikeRust(t *testing.T) {
	host := &taskToolsMCPHost{}
	defer host.close()
	host.attach(&remoteAppServerTUIClient{}, appserver.ThreadStartParams{}, nil)
	config := host.configValues()["mcp_servers."+DynamicToolNamespace].(map[string]any)

	listed := taskToolsMCPPost(t, config, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	for _, raw := range listed["result"].(map[string]any)["tools"].([]any) {
		name := raw.(map[string]any)["name"].(string)
		if isManagedWorktreeTool(name) {
			t.Fatalf("tools/list advertised %q without a worktree service", name)
		}
	}

	response := taskToolsMCPPost(t, config, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "list_worktrees",
			"arguments": map[string]any{},
			"_meta":     map[string]any{"threadId": "thread-a"},
		},
	})
	rpcError := response["error"].(map[string]any)
	if rpcError["message"] != managedWorktreeUnavailableOnConnection {
		t.Fatalf("worktree call error = %#v, want %q", rpcError, managedWorktreeUnavailableOnConnection)
	}
}

func TestManagedWorktreeOperationRegistryLikeRust(t *testing.T) {
	now := time.Unix(1_000, 0)
	tools := &managedWorktreeTools{
		now:        func() time.Time { return now },
		operations: map[string]*managedWorktreeOperation{},
	}
	first, pending, err := tools.reserveOperation("thread-a")
	if err != nil || pending != "" || first == "" {
		t.Fatalf("first reserve = %q, %q, %v", first, pending, err)
	}
	reused, pending, err := tools.reserveOperation("thread-a")
	if err != nil || reused != "" || !strings.Contains(pending, first) {
		t.Fatalf("pending reserve = %q, %q, %v; want operation %q", reused, pending, err, first)
	}
	other, pending, err := tools.reserveOperation("thread-b")
	if err != nil || pending != "" || other == "" || other == first {
		t.Fatalf("other-thread reserve = %q, %q, %v", other, pending, err)
	}
	if text, ok := tools.executeTool(context.Background(), "thread-b", "get_worktree_creation_status", json.RawMessage(`{"operationId":"`+first+`"}`)); ok || !strings.Contains(text, managedWorktreeUnknownOperationMessage) {
		t.Fatalf("cross-thread status = %q, %v", text, ok)
	}

	tools.operations[first].result = &managedWorktreeOperationResult{value: json.RawMessage(`{"type":"created"}`)}
	if text, ok := tools.executeTool(context.Background(), "thread-a", "get_worktree_creation_status", json.RawMessage(`{"operationId":"`+first+`"}`)); !ok || text != `{"type":"created"}` {
		t.Fatalf("completed status = %q, %v", text, ok)
	}
	now = now.Add(managedWorktreeOperationRetention + time.Second)
	if _, _, err := tools.reserveOperation("thread-c"); err != nil {
		t.Fatalf("reserve after retention: %v", err)
	}
	if _, ok := tools.operations[first]; ok {
		t.Fatal("completed operation older than the retention window was not pruned")
	}
}

func TestManagedWorktreeOperationRegistryBoundsLikeRust(t *testing.T) {
	tools := &managedWorktreeTools{now: time.Now, operations: map[string]*managedWorktreeOperation{}}
	for index := 0; index < managedWorktreeMaxOperations; index++ {
		if _, pending, err := tools.reserveOperation("thread-" + string(rune('a'+index))); err != nil || pending != "" {
			t.Fatalf("reserve %d = %q, %v", index, pending, err)
		}
	}
	_, _, err := tools.reserveOperation("thread-overflow")
	if err == nil || err.Error() != managedWorktreeTooManyOperationsMessage {
		t.Fatalf("overflow error = %v, want %q", err, managedWorktreeTooManyOperationsMessage)
	}
}

func TestManagedWorktreeArgumentsLikeRust(t *testing.T) {
	tools := &managedWorktreeTools{now: time.Now, operations: map[string]*managedWorktreeOperation{}}
	cases := []struct {
		name      string
		arguments string
		want      string
	}{
		{name: "sync", arguments: `{"allowAsync":false}`, want: managedWorktreeInvalidCreateArgsMessage},
		{name: "long ref", arguments: `{"allowAsync":true,"ref":"` + strings.Repeat("x", managedWorktreeMaxRevisionBytes+1) + `"}`, want: managedWorktreeInvalidCreateArgsMessage},
		{name: "duplicate alias", arguments: `{"allowAsync":true,"async":true}`, want: "invalid arguments"},
		{name: "trailing document", arguments: `{"allowAsync":true} {}`, want: "invalid arguments"},
		{name: "unknown field", arguments: `{"allowAsync":true,"extra":1}`, want: "unknown field"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			text, ok := tools.executeTool(context.Background(), "thread-a", "create_worktree", json.RawMessage(testCase.arguments))
			if ok {
				t.Fatalf("arguments unexpectedly succeeded: %s", text)
			}
			if !strings.Contains(text, testCase.want) {
				t.Fatalf("error = %s, want %q", text, testCase.want)
			}
		})
	}

	if text, ok := tools.executeTool(context.Background(), "thread-a", "list_worktrees", json.RawMessage(strings.Repeat(" ", managedWorktreeMaxArgumentBytes+1))); ok || !strings.Contains(text, managedWorktreeArgumentsTooLargeMessage) {
		t.Fatalf("oversized arguments = %q, %v", text, ok)
	}
}

func TestManagedWorktreeResultLimitLikeRust(t *testing.T) {
	tools := &managedWorktreeTools{
		operations: map[string]*managedWorktreeOperation{
			"operation": {
				threadID: "thread-a",
				result:   &managedWorktreeOperationResult{value: json.RawMessage(`"` + strings.Repeat("x", managedWorktreeMaxResultBytes) + `"`)},
			},
		},
	}
	text, ok := tools.executeTool(context.Background(), "thread-a", "get_worktree_creation_status", json.RawMessage(`{"operationId":"operation"}`))
	if ok || text != managedWorktreeDisplayLimitMessage {
		t.Fatalf("oversized result = %q, %v", text, ok)
	}
}

func TestRemoteTUIToolsNamespaceConflictUsesHostedNamespaceLikeRust(t *testing.T) {
	values := map[string]any{"mcp_servers": map[string]any{
		"codex_tui":        map[string]any{},
		"codex_worktrees":  map[string]any{},
		"unrelated_server": map[string]any{},
	}}
	if !remoteTUIToolsNamespaceConflict(values, DynamicToolNamespace) {
		t.Fatal("codex_tui namespace was not reported as a conflict")
	}
	if !remoteTUIToolsNamespaceConflict(values, "codex_worktrees") {
		t.Fatal("codex_worktrees namespace was not reported as a conflict")
	}
	if !remoteTUIToolsNamespaceConflict(values, "unrelated_server") {
		t.Fatal("the configured unrelated namespace was not reported as a conflict")
	}

	localDaemonValues := map[string]any{"mcp_servers": map[string]any{"codex_worktrees": map[string]any{}}}
	if remoteTUIToolsNamespaceConflict(localDaemonValues, DynamicToolNamespace) {
		t.Fatal("a codex_worktrees server must not block the local-daemon codex_tui host")
	}
	if remoteTUIToolsNamespaceConflict(nil, DynamicToolNamespace) {
		t.Fatal("an empty config reported a namespace conflict")
	}
}
