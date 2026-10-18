package mcp

import "testing"

func TestRuntimeConfigParsesToolInputSchemaMaxBytesLikeRust(t *testing.T) {
	runtimeConfig := RuntimeConfigFromValues(map[string]any{
		"mcp_servers": map[string]any{
			"docs": map[string]any{
				"url":                         "https://mcp.example.test/mcp",
				"tool_input_schema_max_bytes": int64(12_000),
			},
		},
	}, "/codex/home")
	config := runtimeConfig.Servers["docs"].Config
	if config.ToolInputSchemaMaxBytes == nil || *config.ToolInputSchemaMaxBytes != 12_000 {
		t.Fatalf("ToolInputSchemaMaxBytes = %#v", config.ToolInputSchemaMaxBytes)
	}
	if got := config.ToolInputSchemaCompactionBytes(); got != 12_000 {
		t.Fatalf("ToolInputSchemaCompactionBytes() = %d", got)
	}
}
