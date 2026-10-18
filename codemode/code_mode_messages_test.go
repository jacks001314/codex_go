package codemode

import (
	"strings"
	"testing"

	"codex_go/model"
)

func TestExecDescriptionCatalogOverridesLikeRust(t *testing.T) {
	execText := "Custom exec {{ default_exec_yield_time_ms }} and {{ image_helper }}"
	guidance := ""
	preamble := "type Custom = string;"
	messages := &model.CodeModeToolMessages{
		Exec:                        &model.CodeModeToolMessage{Description: &execText},
		DeferredNestedToolsGuidance: &guidance,
		McpTypescriptPreamble:       &preamble,
	}
	description := BuildExecToolDescriptionWithOptions(nil, nil, nil, true, true, messages, 1234, "custom-image-helper")
	if !strings.HasPrefix(description, "Custom exec 1234 and custom-image-helper") {
		t.Fatalf("description = %q", description)
	}
	if strings.Contains(description, "Some deferred nested tools may be omitted") {
		t.Fatal("empty guidance override did not suppress the bundled text")
	}
	if !strings.Contains(description, "Shared MCP Types:\n```ts\ntype Custom = string;") {
		t.Fatalf("custom MCP preamble missing:\n%s", description)
	}
}

func TestWaitToolOverridesLikeRust(t *testing.T) {
	description := "Custom wait"
	parameters := `{"type":"object","required":["cell_id"],"properties":{"cell_id":{"type":"string"}}}`
	messages := &model.CodeModeToolMessages{Wait: &model.CodeModeToolMessage{Description: &description, Parameters: &parameters}}
	wait := BuildWaitToolWithMessages(messages)
	if wait.Description != description {
		t.Fatalf("wait description = %q", wait.Description)
	}
	if wait.Parameters["type"] != "object" || wait.Parameters["required"] == nil {
		t.Fatalf("wait parameters = %#v", wait.Parameters)
	}
}
