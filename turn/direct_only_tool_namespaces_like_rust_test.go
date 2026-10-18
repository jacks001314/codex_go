package turn

import (
	"context"
	"testing"

	"codex_go/tool"
)

func registerDirectOnlyTestTool(t *testing.T, registry *tool.Registry, name tool.ToolName, exposure tool.Exposure) {
	t.Helper()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: name, Exposure: exposure}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{}, nil
	})); err != nil {
		t.Fatalf("Register(%q) error = %v", name.Key(), err)
	}
}

// Rust #50687/#51704 follow-up: direct-only namespaces bypass deferral and the
// nested Code Mode surface while hidden tools stay hidden.
func TestDirectOnlyToolNamespaceOverridesLikeRust(t *testing.T) {
	registry := tool.NewRegistry()
	registerDirectOnlyTestTool(t, registry, tool.NamespacedName("mcp__environment", "read"), tool.ExposureDiscoverable)
	registerDirectOnlyTestTool(t, registry, tool.NamespacedName("mcp__environment", "hidden"), tool.ExposureHidden)
	registerDirectOnlyTestTool(t, registry, tool.NamespacedName("mcp__other", "read"), tool.ExposureDiscoverable)
	registerDirectOnlyTestTool(t, registry, tool.PlainName("exec_command"), tool.ExposureModelVisible)

	applyDirectOnlyToolNamespaceOverrides(registry, []string{" mcp__environment ", "functions", " "})

	for _, testCase := range []struct {
		name tool.ToolName
		want tool.Exposure
	}{
		{tool.NamespacedName("mcp__environment", "read"), tool.ExposureDirectModelOnly},
		{tool.NamespacedName("mcp__environment", "hidden"), tool.ExposureHidden},
		{tool.NamespacedName("mcp__other", "read"), tool.ExposureDiscoverable},
		{tool.PlainName("exec_command"), tool.ExposureDirectModelOnly},
	} {
		spec, ok := registry.Spec(testCase.name)
		if !ok || spec.Exposure != testCase.want {
			t.Fatalf("%q exposure = %#v, want %q", testCase.name.Key(), spec.Exposure, testCase.want)
		}
	}
	environmentRead := tool.NamespacedName("mcp__environment", "read")
	for _, spec := range registry.DiscoverableSpecs() {
		if spec.Name.Key() == environmentRead.Key() {
			t.Fatal("a direct-only tool remained searchable")
		}
	}
}

// The configured namespace list reaches the built registry and removes plain
// built-in tools from deferral under the default "functions" namespace.
func TestBuildToolRegistryAppliesDirectOnlyNamespacesLikeRust(t *testing.T) {
	base, err := BuildToolRegistry(DefaultToolRegistryOptions(t.TempDir()))
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	var candidate tool.ToolName
	for _, spec := range base.ModelVisibleSpecs() {
		// The Code Mode exec/wait tools are registered after Rust's namespace
		// override pass, so they are not valid targets for this assertion.
		if spec.Name.Namespace == "" && spec.Name.Name != tool.CodeModeExecToolName && spec.Name.Name != "wait" &&
			tool.IsCodeModeAvailable(spec.Exposure) {
			candidate = spec.Name
			break
		}
	}
	if candidate.Key() == "" {
		t.Fatal("no code-mode available plain tool in the default registry")
	}
	options := DefaultToolRegistryOptions(t.TempDir())
	options.DirectOnlyToolNamespaces = []string{"functions"}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	spec, ok := registry.Spec(candidate)
	if !ok || spec.Exposure != tool.ExposureDirectModelOnly {
		t.Fatalf("%q exposure = %q, want direct_model_only", candidate.Key(), spec.Exposure)
	}
}

// Rust keeps excluded namespaces registered but removes them from the nested
// Code Mode surface.
func TestBuildToolRegistryAppliesExcludedNamespacesLikeRust(t *testing.T) {
	options := DefaultToolRegistryOptions(t.TempDir())
	options.ExcludedToolNamespaces = []string{"functions"}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	executor, ok := registry.Lookup(tool.PlainName(tool.CodeModeExecToolName))
	if !ok {
		t.Fatal("code mode exec tool is not registered")
	}
	specProvider, ok := executor.(interface{ CodeModeToolSpecs() []tool.Spec })
	if !ok {
		t.Fatalf("code mode exec executor = %T, want CodeModeToolSpecs", executor)
	}
	for _, spec := range specProvider.CodeModeToolSpecs() {
		if spec.Name.Namespace == "" {
			t.Fatalf("excluded plain tool %q remained in the nested surface", spec.Name.Key())
		}
	}
	if _, ok := registry.Spec(tool.PlainName("get_context_remaining")); !ok {
		t.Fatal("plain tools must stay registered for direct access")
	}
}
