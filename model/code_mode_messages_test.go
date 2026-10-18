package model

import (
	"encoding/json"
	"testing"
)

func TestCodeModeToolMessagesParseLikeRust(t *testing.T) {
	var messages ModelMessages
	if err := json.Unmarshal([]byte(`{
		"tools": {
			"code_mode": {
				"exec": {"description": "Custom exec {{ default_exec_yield_time_ms }} {{ image_helper }}"},
				"wait": {"description": "Custom wait", "parameters": "{\"type\":\"object\",\"properties\":{\"cell_id\":{\"type\":\"string\"}}}"},
				"deferred_nested_tools_guidance": "",
				"mcp_typescript_preamble": "type Custom = string;"
			}
		}
	}`), &messages); err != nil {
		t.Fatal(err)
	}
	codeMode := messages.CodeMode()
	if codeMode == nil || codeMode.Exec == nil || codeMode.Exec.Description == nil ||
		*codeMode.Exec.Description != "Custom exec {{ default_exec_yield_time_ms }} {{ image_helper }}" {
		t.Fatalf("code mode exec = %#v", codeMode)
	}
	if codeMode.Wait == nil || codeMode.Wait.Description == nil || *codeMode.Wait.Description != "Custom wait" {
		t.Fatalf("code mode wait = %#v", codeMode.Wait)
	}
	if codeMode.DeferredNestedToolsGuidance == nil || *codeMode.DeferredNestedToolsGuidance != "" {
		t.Fatalf("deferred guidance = %#v, want explicit empty", codeMode.DeferredNestedToolsGuidance)
	}
	if codeMode.McpTypescriptPreamble == nil || *codeMode.McpTypescriptPreamble != "type Custom = string;" {
		t.Fatalf("mcp preamble = %#v", codeMode.McpTypescriptPreamble)
	}
}
