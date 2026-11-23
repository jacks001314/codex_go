package turn

import (
	"context"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

// Rust #62534: builtin namespace guidance annotates the Code Mode namespace
// header once and leaves the flat tool listing untouched.
func TestBuildToolRegistryAppliesBuiltinNamespacePrefixesToHeadersLikeRust(t *testing.T) {
	runtime := tool.NewCodeModeRuntime(nil, false)
	options := &ToolRegistryOptions{
		EnableCodeMode:  true,
		CodeModeRuntime: runtime,
		IndirectDescriptionPrefixes: &tool.IndirectDescriptionPrefixes{
			BuiltinToolNamespaces: map[string]string{"functions": "  Catalog guidance.  "},
		},
		ExtraTools: []tool.Executor{
			tool.NewExecutorFunc(tool.Spec{
				Name:        tool.PlainName("lookup"),
				Description: "Lookup.",
				Exposure:    tool.ExposureModelVisible,
			}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
				return &tool.Output{Success: true, Body: "ok"}, nil
			}),
		},
	}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	for _, spec := range tool.NewRouter(registry).CodeModeToolSpecs() {
		if spec.Name.Name == "lookup" && spec.Description != "Lookup." {
			t.Fatalf("flat code mode description = %q, builtin guidance must not repeat", spec.Description)
		}
	}
	resolved, err := tool.NewIndirectNamespacePrefixes(
		options.IndirectDescriptionPrefixes,
		registry.MCPServerNamespaces(),
		registry.BuiltinToolNamespaces(),
	)
	if err != nil {
		t.Fatalf("NewIndirectNamespacePrefixes() error = %v", err)
	}
	execSpec, ok := registry.Spec(tool.PlainName(tool.CodeModeExecToolName))
	if !ok {
		t.Fatal("code-mode exec spec is missing")
	}
	promptSpecs := codeModeOnlyExecPromptSpecs([]tool.Spec{execSpec}, tool.NewRouter(registry).CodeModeToolSpecs(), resolved)
	if len(promptSpecs) == 0 {
		t.Fatal("code-mode prompt spec is missing")
	}
	description := promptSpecs[0].Description
	if !strings.Contains(description, "## functions\nCatalog guidance.") {
		t.Fatalf("functions header is missing builtin guidance:\n%s", description)
	}
	if strings.Contains(description, "Catalog guidance.\n\nLookup.") {
		t.Fatalf("builtin guidance was rendered as a flat entry prefix:\n%s", description)
	}
}

// Rust #62534: the selected model's four prefix tables share one budget that
// is validated before any tool availability is resolved.
func TestBuildToolRegistryValidatesDescriptionPrefixBudgetLikeRust(t *testing.T) {
	limit := func(value int) *int { return &value }
	catalog := func(extra string, budget *int) *model.ToolMessages {
		return &model.ToolMessages{
			DescriptionPrefixBudgetBytes: budget,
			FunctionsNamespaceFunctionsDescriptionPrefixes: map[string]string{
				"exec":       " " + strings.Repeat("é", 254) + extra + " ",
				"view_image": "v",
			},
			IndirectDescriptionPrefixes: &model.IndirectDescriptionPrefixes{
				Namespaces:            map[string]string{"mcp__reports_one": "m"},
				BuiltinToolNamespaces: map[string]string{"unavailable": "b"},
				MCPServers:            map[string]string{"reports.one": "m"},
			},
		}
	}
	if _, err := BuildToolRegistry(&ToolRegistryOptions{ModelToolMessages: catalog("", nil)}); err != nil {
		t.Fatalf("at-budget registry error = %v", err)
	}
	_, err := BuildToolRegistry(&ToolRegistryOptions{ModelToolMessages: catalog("x", nil)})
	if err == nil || err.Error() != "Model catalog tool description prefixes total 513 UTF-8 bytes, exceeding description_prefix_budget_bytes (512)" {
		t.Fatalf("over-budget registry error = %v", err)
	}
	if _, err := BuildToolRegistry(&ToolRegistryOptions{ModelToolMessages: catalog("x", limit(513))}); err != nil {
		t.Fatalf("raised-budget registry error = %v", err)
	}
}
