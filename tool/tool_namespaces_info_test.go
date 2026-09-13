package tool

import (
	"context"
	"reflect"
	"testing"
)

type stubMCPServerExecutor struct {
	spec       Spec
	serverName string
}

func (e stubMCPServerExecutor) Spec() Spec            { return e.spec }
func (e stubMCPServerExecutor) MCPServerName() string { return e.serverName }
func (e stubMCPServerExecutor) Execute(_ context.Context, _ *Invocation) (*Output, error) {
	return &Output{Success: true}, nil
}

// Rust collect_tool_namespaces_info: direct model-visible functions, Code Mode
// names, deferred model-only tools, MCP ownership, and the dropped entries.
func TestCollectToolNamespacesInfoLikeRust(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("exec_command")}, nil)); err != nil {
		t.Fatalf("register exec_command: %v", err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("hidden_tool"), Exposure: ExposureHidden}, nil)); err != nil {
		t.Fatalf("register hidden_tool: %v", err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("unlisted_tool"), Exposure: ExposureDiscoverable}, nil)); err != nil {
		t.Fatalf("register unlisted_tool: %v", err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("deferred_tool"), Exposure: ExposureDeferredModelOnly, Search: &SearchInfo{Text: "deferred"}}, nil)); err != nil {
		t.Fatalf("register deferred_tool: %v", err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("deferred_plain"), Exposure: ExposureDeferredModelOnly}, nil)); err != nil {
		t.Fatalf("register deferred_plain: %v", err)
	}
	mcpSpec := Spec{Name: NamespacedName("sample", "search")}
	if err := registry.Register(stubMCPServerExecutor{spec: mcpSpec, serverName: "sample"}); err != nil {
		t.Fatalf("register mcp tool: %v", err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName(ToolSearchName), Search: &SearchInfo{Text: "search"}, Exposure: ExposureModelVisible}, nil)); err != nil {
		t.Fatalf("register tool search: %v", err)
	}

	codeModeNames := map[string]CodeModeToolNameMetadata{
		"exec_command":   {Name: "exec_command"},
		"sample__search": {Name: "search", Namespace: stringPtr("sample")},
	}
	info := CollectToolNamespacesInfo(registry, codeModeNames, []Spec{
		{Name: PlainName("exec_command")},
		{Name: NamespacedName("sample", "search")},
		{Name: PlainName(ToolSearchName), Search: &SearchInfo{Text: "search"}},
	})

	execFunctions := info[DefaultFunctionNamespace]
	if execFunctions.Name != DefaultFunctionNamespace {
		t.Fatalf("default namespace = %#v", execFunctions)
	}
	exec, ok := execFunctions.Functions["exec_command"]
	if !ok || !exec.Direct || exec.Deferred || exec.Source.Kind != ToolFunctionSourceHarness {
		t.Fatalf("exec_command entry = %#v", exec)
	}
	if exec.CodeModeName == nil || *exec.CodeModeName != "exec_command" {
		t.Fatalf("exec_command code mode name = %#v", exec.CodeModeName)
	}
	if deferred, ok := execFunctions.Functions["deferred_tool"]; !ok || !deferred.Deferred || deferred.Direct {
		t.Fatalf("deferred_tool entry = %#v", deferred)
	}
	if _, ok := execFunctions.Functions["deferred_plain"]; ok {
		t.Fatalf("a deferred tool without search info must be dropped: %#v", execFunctions.Functions)
	}
	if _, ok := execFunctions.Functions["hidden_tool"]; ok {
		t.Fatalf("a hidden tool must be dropped: %#v", execFunctions.Functions)
	}
	if _, ok := execFunctions.Functions["unlisted_tool"]; ok {
		t.Fatalf("an undisclosed tool must be dropped: %#v", execFunctions.Functions)
	}

	search := info[ToolSearchName].Functions[ToolSearchFunctionName]
	if !search.Direct || search.Name != ToolSearchFunctionName {
		t.Fatalf("tool search entry = %#v", search)
	}

	sample := info["sample"].Functions["search"]
	if !sample.Direct || sample.Source.Kind != ToolFunctionSourceMCP || sample.Source.ServerName != "sample" {
		t.Fatalf("mcp entry = %#v", sample)
	}
	if sample.CodeModeName == nil || *sample.CodeModeName != "sample__search" {
		t.Fatalf("mcp code mode name = %#v", sample.CodeModeName)
	}

	// Without a visible tool search, deferred tools are no longer deferred.
	withoutSearch := CollectToolNamespacesInfo(registry, nil, []Spec{{Name: PlainName("deferred_tool"), Search: &SearchInfo{Text: "deferred"}}})
	if entry, ok := withoutSearch[DefaultFunctionNamespace].Functions["deferred_tool"]; ok && entry.Deferred {
		t.Fatalf("deferred tool reported without a visible tool search: %#v", entry)
	}
	if len(CollectToolNamespacesInfo(nil, nil, nil)) != 0 {
		t.Fatal("a nil registry produced an inventory")
	}
	if !reflect.DeepEqual(CollectToolNamespacesInfo(NewRegistry(), nil, nil), ToolNamespacesInfo{}) {
		t.Fatal("an empty registry produced entries")
	}
}
