package mcp

import (
	"strings"
	"testing"
)

func TestMCPToolExecutorHonorsSchemaCompactionBudgetLikeRust(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Parameter guidance for the query. " + strings.Repeat("detail ", 1800),
			},
		},
	}
	defaultExecutor := NewToolExecutor(&ToolExecutorOptions{
		ServerName: "docs",
		ToolInfo:   &MCPToolInfo{Name: "search", InputSchema: schema},
	})
	defaultQuery := defaultExecutor.Spec().InputSchema["properties"].(map[string]any)["query"].(map[string]any)
	if _, ok := defaultQuery["description"]; ok {
		t.Fatalf("default executor retained oversized description: %#v", defaultQuery)
	}
	largeExecutor := NewToolExecutor(&ToolExecutorOptions{
		ServerName:                  "docs",
		ToolInfo:                    &MCPToolInfo{Name: "search", InputSchema: schema},
		ToolInputSchemaMaxBytes:     32_000,
		CodeModeInputSchemaMaxBytes: 32_000,
	})
	spec := largeExecutor.Spec()
	largeQuery := spec.InputSchema["properties"].(map[string]any)["query"].(map[string]any)
	if description, _ := largeQuery["description"].(string); !strings.Contains(description, "Parameter guidance") {
		t.Fatalf("large executor lost parameter guidance: %#v", largeQuery)
	}
	if spec.InputSchemaMaxBytes < 32_000 {
		t.Fatalf("Code Mode schema budget = %d", spec.InputSchemaMaxBytes)
	}
}
