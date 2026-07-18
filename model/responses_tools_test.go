package model

import (
	"encoding/json"
	"testing"

	"codex_go/tool"
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
			OutputSchema: map[string]any{"type": "object", "required": []string{"output"}},
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
	if outputSchema, ok := tools[0]["output_schema"].(map[string]any); !ok || outputSchema["type"] != "object" {
		t.Fatalf("function output schema = %#v", tools[0]["output_schema"])
	}
	if tools[1]["type"] != "tool_search" || tools[1]["execution"] != "client" {
		t.Fatalf("tool_search = %#v", tools[1])
	}
	format, ok := tools[2]["format"].(map[string]any)
	if !ok || tools[2]["type"] != "custom" || tools[2]["name"] != tool.DefaultApplyPatchToolName || format["type"] != "grammar" {
		t.Fatalf("freeform tool = %#v", tools[2])
	}
}

func TestResponsesMCPToolsUseNamespaceLikeRust(t *testing.T) {
	got := ResponsesToolsFromSpecs([]tool.Spec{{
		Name: tool.NamespacedName("mcp__memory", "create_entities"),
	}})
	if len(got) != 1 {
		t.Fatalf("tools = %#v", got)
	}
	item, ok := got[0].(map[string]any)
	if !ok || item["type"] != "namespace" || item["name"] != "mcp__memory" {
		t.Fatalf("tool = %#v", got[0])
	}
	children, ok := item["tools"].([]map[string]any)
	if !ok || len(children) != 1 || children[0]["name"] != "create_entities" {
		t.Fatalf("namespace children = %#v", item["tools"])
	}
}

func TestResponsesToolsFromSpecsSerializesClockNamespaceLikeRust(t *testing.T) {
	got := ResponsesToolsFromSpecs([]tool.Spec{{
		Name:                 tool.NamespacedName("clock", "curr_time"),
		Description:          "Return the current time in UTC.",
		NamespaceDescription: "Tools for reading and waiting on time.",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}})
	if len(got) != 1 {
		t.Fatalf("tools = %#v", got)
	}
	namespace, ok := got[0].(map[string]any)
	if !ok || namespace["type"] != "namespace" || namespace["name"] != "clock" || namespace["description"] != "Tools for reading and waiting on time." {
		t.Fatalf("namespace = %#v", got[0])
	}
	tools, ok := namespace["tools"].([]map[string]any)
	if !ok || len(tools) != 1 || tools[0]["name"] != "curr_time" || tools[0]["type"] != "function" {
		t.Fatalf("namespace tools = %#v", namespace["tools"])
	}
	if _, ok := tools[0]["strict"]; ok {
		t.Fatalf("clock namespace tool should not include strict: %#v", tools[0])
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

func TestResponsesToolsFromSpecsSerializesStandaloneWebSearchNamespace(t *testing.T) {
	got := ResponsesToolsFromSpecs([]tool.Spec{{
		Name:                 tool.NamespacedName("web", "run"),
		Description:          "Tool for accessing the internet.",
		NamespaceDescription: "Tool for accessing the internet.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"time": map[string]any{"type": "array", "description": "Get time for the given UTC offsets."},
			},
		},
	}})
	if len(got) != 1 {
		t.Fatalf("tools = %#v", got)
	}
	namespace, ok := got[0].(map[string]any)
	if !ok || namespace["type"] != "namespace" || namespace["name"] != "web" {
		t.Fatalf("namespace = %#v", got[0])
	}
	tools, ok := namespace["tools"].([]map[string]any)
	if !ok || len(tools) != 1 || tools[0]["name"] != "run" || tools[0]["type"] != "function" {
		t.Fatalf("namespace tools = %#v", namespace["tools"])
	}
	properties, ok := tools[0]["parameters"].(map[string]any)["properties"].(map[string]any)
	if !ok {
		t.Fatalf("parameters = %#v", tools[0]["parameters"])
	}
	timeSchema, ok := properties["time"].(map[string]any)
	if !ok || timeSchema["description"] != "Get time for the given UTC offsets." {
		t.Fatalf("time schema = %#v", properties["time"])
	}
}

func TestResponsesToolsFromSpecsSerializesImageGenerationNamespace(t *testing.T) {
	got := ResponsesToolsFromSpecs([]tool.Spec{{
		Name:                 tool.NamespacedName("image_gen", "imagegen"),
		Description:          "Generate images",
		NamespaceDescription: "Tools in the image_gen namespace.",
		InputSchema: map[string]any{
			"type":       "object",
			"required":   []string{"prompt"},
			"properties": map[string]any{"prompt": map[string]any{"type": "string"}},
		},
	}})
	if len(got) != 1 {
		t.Fatalf("tools = %#v", got)
	}
	namespace, ok := got[0].(map[string]any)
	if !ok || namespace["type"] != "namespace" || namespace["name"] != "image_gen" || namespace["description"] != "Tools in the image_gen namespace." {
		t.Fatalf("namespace = %#v", got[0])
	}
	tools, ok := namespace["tools"].([]map[string]any)
	if !ok || len(tools) != 1 || tools[0]["name"] != "imagegen" || tools[0]["type"] != "function" {
		t.Fatalf("namespace tools = %#v", namespace["tools"])
	}
	if _, ok := tools[0]["strict"]; ok {
		t.Fatalf("image_gen namespace tool should not include strict: %#v", tools[0])
	}
}

func TestResponsesLoadableToolsFromSpecsSerializesSearchOutputLikeRust(t *testing.T) {
	got := ResponsesLoadableToolsFromSpecs([]tool.Spec{
		{
			Name:                 tool.NamespacedName("angr", "am_list_functions"),
			Description:          "List functions",
			NamespaceDescription: "angr MCP tools",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"filter": map[string]any{"type": "string"}},
			},
			Exposure: tool.ExposureDiscoverable,
		},
		{
			Name:        tool.PlainName("plain_lookup"),
			Description: "Plain lookup",
			Exposure:    tool.ExposureDiscoverable,
		},
	})
	if len(got) != 2 {
		t.Fatalf("tools = %#v", got)
	}
	namespace, ok := got[0].(map[string]any)
	if !ok || namespace["type"] != "namespace" || namespace["name"] != "angr" || namespace["description"] != "angr MCP tools" {
		t.Fatalf("namespace = %#v", got[0])
	}
	children, ok := namespace["tools"].([]map[string]any)
	if !ok || len(children) != 1 {
		t.Fatalf("namespace children = %#v", namespace["tools"])
	}
	if children[0]["type"] != "function" || children[0]["name"] != "am_list_functions" || children[0]["defer_loading"] != true || children[0]["strict"] != false {
		t.Fatalf("child = %#v", children[0])
	}
	plain, ok := got[1].(map[string]any)
	if !ok || plain["type"] != "function" || plain["name"] != "plain_lookup" || plain["defer_loading"] != true || plain["strict"] != false {
		t.Fatalf("plain tool = %#v", got[1])
	}
}
