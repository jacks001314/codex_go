package mcp

import (
	"strings"
	"testing"
)

func TestRuntimeToolsFromStatusesPreparesCodexAppsFileParamsLikeRust(t *testing.T) {
	rawFileSchema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"file_id":      map[string]any{"type": "string"},
			"download_url": map[string]any{"type": "string"},
			"mime_type":    map[string]any{"type": "string"},
		},
	}
	tools := RuntimeToolsFromStatuses([]MCPServerStatus{{
		Name:  CodexAppsServerName,
		State: MCPServerReady,
		Tools: []MCPToolInfo{{
			Name: "capture",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file": map[string]any{"description": "Original payload.", "$ref": "#/$defs/file"},
					"files": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "object", "additionalProperties": true},
					},
				},
				"$defs": map[string]any{"file": rawFileSchema},
			},
			Meta: map[string]any{"openai/fileParams": []any{"file", "files"}},
		}},
	}})
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", tools)
	}
	info := tools[0]
	if got := info.OpenAIFileInputOptionalFields["file"]; len(got) != 1 || got[0] != "mime_type" {
		t.Fatalf("file optional fields = %#v", got)
	}
	if got := info.OpenAIFileInputOptionalFields["files"]; len(got) != 2 || got[0] != "mime_type" || got[1] != "file_name" {
		t.Fatalf("files optional fields = %#v", got)
	}
	properties := info.Tool.InputSchema["properties"].(map[string]any)
	file := properties["file"].(map[string]any)
	if file["type"] != "string" || file["$ref"] != nil || !strings.Contains(file["description"].(string), openAIFileLocalPathGuidance) {
		t.Fatalf("file schema = %#v", file)
	}
	files := properties["files"].(map[string]any)
	items := files["items"].(map[string]any)
	if files["type"] != "array" || items["type"] != "string" {
		t.Fatalf("files schema = %#v", files)
	}
	if rawFileSchema["type"] != "object" {
		t.Fatalf("raw schema was mutated: %#v", rawFileSchema)
	}
}

func TestRuntimeToolsFromStatusesIgnoresFileParamsOnUntrustedMCPServers(t *testing.T) {
	tools := RuntimeToolsFromStatuses([]MCPServerStatus{{
		Name:  "custom",
		State: MCPServerReady,
		Tools: []MCPToolInfo{{
			Name:        "capture",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"file": map[string]any{"type": "object"}}},
			Meta:        map[string]any{"openai/fileParams": []any{"file"}},
		}},
	}})
	if len(tools) != 1 || len(tools[0].OpenAIFileInputOptionalFields) != 0 {
		t.Fatalf("tools = %#v", tools)
	}
	file := tools[0].Tool.InputSchema["properties"].(map[string]any)["file"].(map[string]any)
	if file["type"] != "object" {
		t.Fatalf("untrusted schema was rewritten: %#v", file)
	}
}
