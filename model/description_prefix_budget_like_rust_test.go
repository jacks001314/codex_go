package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #62534: one budget covers all four prefix tables. Each configured
// value counts once in trimmed UTF-8 bytes, including unused selectors.
func TestToolMessagesDescriptionPrefixBudgetLikeRust(t *testing.T) {
	limit := func(value int) *int { return &value }
	messages := func(extra string, budget *int) *ToolMessages {
		return &ToolMessages{
			DescriptionPrefixBudgetBytes: budget,
			FunctionsNamespaceFunctionsDescriptionPrefixes: map[string]string{
				"exec":       " " + strings.Repeat("é", 254) + extra + " ",
				"view_image": "v",
			},
			IndirectDescriptionPrefixes: &IndirectDescriptionPrefixes{
				Namespaces:            map[string]string{"mcp__reports_one": "m"},
				BuiltinToolNamespaces: map[string]string{"unavailable": "b"},
				MCPServers:            map[string]string{"reports.one": "m"},
			},
		}
	}

	if err := messages("", nil).ValidateDescriptionPrefixBudget(); err != nil {
		t.Fatalf("at default budget error = %v", err)
	}
	err := messages("x", nil).ValidateDescriptionPrefixBudget()
	if err == nil || err.Error() != "Model catalog tool description prefixes total 513 UTF-8 bytes, exceeding description_prefix_budget_bytes (512)" {
		t.Fatalf("over-budget error = %v", err)
	}
	if err := messages("x", limit(513)).ValidateDescriptionPrefixBudget(); err != nil {
		t.Fatalf("raised budget error = %v", err)
	}
	var nilMessages *ToolMessages
	if err := nilMessages.ValidateDescriptionPrefixBudget(); err != nil {
		t.Fatalf("nil messages error = %v", err)
	}
}

func TestToolMessagesParseBuiltinNamespacePrefixesAndBudgetLikeRust(t *testing.T) {
	var tools ToolMessages
	raw := `{
		"description_prefix_budget_bytes": 640,
		"functions_namespace_functions_description_prefixes": {"exec": "Run code."},
		"indirect_description_prefixes": {
			"namespaces": {"functions": "Catalog."},
			"builtin_tool_namespaces": {"functions": "Catalog.", "time": "Time guidance."},
			"mcp_servers": {"reports.one": "Reports."}
		}
	}`
	if err := json.Unmarshal([]byte(raw), &tools); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if tools.DescriptionPrefixBudgetBytes == nil || *tools.DescriptionPrefixBudgetBytes != 640 {
		t.Fatalf("budget = %#v", tools.DescriptionPrefixBudgetBytes)
	}
	if tools.IndirectDescriptionPrefixes == nil || tools.IndirectDescriptionPrefixes.BuiltinToolNamespaces["time"] != "Time guidance." {
		t.Fatalf("builtin namespaces = %#v", tools.IndirectDescriptionPrefixes)
	}
	clone := cloneModelInfo(ModelInfo{ModelMessages: &ModelMessages{Tools: &tools}})
	clone.ModelMessages.Tools.IndirectDescriptionPrefixes.BuiltinToolNamespaces["time"] = "changed"
	if tools.IndirectDescriptionPrefixes.BuiltinToolNamespaces["time"] != "Time guidance." {
		t.Fatal("clone shared the builtin namespace map")
	}
}
