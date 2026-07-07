package model

import (
	"encoding/json"
	"testing"

	"codex_go/internal/tool"
)

func TestResponsesToolsFromSpecsSerializesFunctionSearchAndFreeform(t *testing.T) {
	specs := []tool.Spec{
		{
			Name:        tool.PlainName("echo"),
			Description: "Echo text",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"text": map[string]any{"type": "string"}},
			},
		},
		{
			Name:        tool.PlainName(tool.ToolSearchName),
			Description: "Search tools",
			InputSchema: map[string]any{"type": "object"},
		},
		{
			Name:        tool.PlainName(tool.DefaultApplyPatchToolName),
			Description: "Patch files",
			Freeform: &tool.FreeformSpec{
				Syntax:     "lark",
				Definition: `start: "patch"`,
			},
		},
		{
			Name:     tool.PlainName("hidden"),
			Exposure: tool.ExposureHidden,
		},
		{
			Name:     tool.PlainName("discoverable"),
			Exposure: tool.ExposureDiscoverable,
		},
	}

	got := ResponsesToolsFromSpecs(specs)
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var tools []map[string]any
	if err := json.Unmarshal(data, &tools); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("tools = %#v", tools)
	}
	if tools[0]["type"] != "function" || tools[0]["name"] != "echo" || tools[0]["strict"] != false {
		t.Fatalf("function tool = %#v", tools[0])
	}
	if tools[1]["type"] != "tool_search" || tools[1]["execution"] != "client" {
		t.Fatalf("tool_search = %#v", tools[1])
	}
	format, ok := tools[2]["format"].(map[string]any)
	if !ok || tools[2]["type"] != "custom" || tools[2]["name"] != tool.DefaultApplyPatchToolName || format["type"] != "grammar" {
		t.Fatalf("freeform tool = %#v", tools[2])
	}
}

func TestResponsesToolNamesUseCodeModeNamespaceSeparatorForPlainNamespacedSpecs(t *testing.T) {
	got := ResponsesToolsFromSpecs([]tool.Spec{{
		Name: tool.NamespacedName("mcp__memory", "create_entities"),
	}})
	if len(got) != 1 {
		t.Fatalf("tools = %#v", got)
	}
	item, ok := got[0].(map[string]any)
	if !ok || item["name"] != "mcp__memory__create_entities" {
		t.Fatalf("tool = %#v", got[0])
	}
}

func TestResponsesToolsFromSpecsSerializesDynamicNamespace(t *testing.T) {
	got := ResponsesToolsFromSpecs([]tool.Spec{{
		Name:                 tool.NamespacedName("codex_app", "demo_tool"),
		Description:          "Demo dynamic tool",
		NamespaceDescription: "Demo namespace tools",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"city": map[string]any{"type": "string"}},
		},
		Search: &tool.SearchInfo{Source: &tool.SearchSourceInfo{Name: "Dynamic tools"}},
	}})
	if len(got) != 1 {
		t.Fatalf("tools = %#v", got)
	}
	namespace, ok := got[0].(map[string]any)
	if !ok || namespace["type"] != "namespace" || namespace["name"] != "codex_app" || namespace["description"] != "Demo namespace tools" {
		t.Fatalf("namespace = %#v", got[0])
	}
	tools, ok := namespace["tools"].([]map[string]any)
	if !ok || len(tools) != 1 || tools[0]["name"] != "demo_tool" || tools[0]["type"] != "function" {
		t.Fatalf("namespace tools = %#v", namespace["tools"])
	}
	if _, ok := tools[0]["strict"]; ok {
		t.Fatalf("dynamic namespace tool should not include strict: %#v", tools[0])
	}
}
