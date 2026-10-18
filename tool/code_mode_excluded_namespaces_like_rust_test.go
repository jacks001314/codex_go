package tool

import (
	"testing"
)

// Rust #51209: excluded_tool_namespaces removes matching tools from the nested
// JavaScript surface without unregistering them for direct model access.
func TestCodeModeExcludedToolNamespacesLikeRust(t *testing.T) {
	registry := NewRegistry()
	for _, spec := range []Spec{
		{Name: PlainName("update_plan"), Description: "plain tool"},
		{Name: NamespacedName("mcp__hidden", "lookup"), Description: "hidden namespace"},
		{Name: NamespacedName("mcp__visible", "lookup"), Description: "visible namespace"},
	} {
		if err := registry.Register(NewExecutorFunc(spec, noopExecutor)); err != nil {
			t.Fatalf("Register(%q) error = %v", spec.Name.Key(), err)
		}
	}
	runtime := NewCodeModeRuntime(nil, false)
	runtime.SetExcludedToolNamespaces([]string{"functions", "mcp__hidden"})
	exec, wait := runtime.Executors(registry)
	if err := registry.Prepend(wait); err != nil {
		t.Fatal(err)
	}
	if err := registry.Prepend(exec); err != nil {
		t.Fatal(err)
	}
	specs := NewRouter(registry).CodeModeToolSpecs()
	if len(specs) != 1 || specs[0].Name.Key() != "mcp__visible.lookup" {
		t.Fatalf("CodeModeToolSpecs() = %#v, want only the visible namespace", specs)
	}
	for _, name := range []ToolName{PlainName("update_plan"), NamespacedName("mcp__hidden", "lookup")} {
		if _, ok := registry.Lookup(name); !ok {
			t.Fatalf("%q was unregistered from direct access", name.Key())
		}
	}
}
