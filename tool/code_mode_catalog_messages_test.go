package tool

import (
	"strings"
	"testing"
)

func TestCodeModeExecAndWaitCatalogMessagesLikeRust(t *testing.T) {
	execText := "Custom exec {{ default_exec_yield_time_ms }} / {{ image_helper }}"
	waitText := "Custom wait"
	waitParameters := `{"type":"object","required":["cell_id"],"properties":{"cell_id":{"type":"string"},"terminate":{"type":"boolean"}}}`
	messages := &CodeModeToolMessages{
		Exec: &CodeModeToolMessage{Description: &execText},
		Wait: &CodeModeToolMessage{Description: &waitText, Parameters: &waitParameters},
	}
	description := codeModeExecDescription(nil, ToolName{}, 4321, messages)
	if !strings.HasPrefix(description, "Custom exec 4321 / image(value), audio(value), generatedImage(value)") {
		t.Fatalf("exec description = %q", description)
	}
	wait := (&codeModeWaitExecutor{exec: &codeModeExecExecutor{messages: messages}}).Spec()
	if wait.Description != waitText {
		t.Fatalf("wait description = %q", wait.Description)
	}
	if wait.InputSchema["type"] != "object" || wait.InputSchema["required"] == nil {
		t.Fatalf("wait schema = %#v", wait.InputSchema)
	}
}

func TestCodeModeWaitCatalogSchemaFallsBackLikeRust(t *testing.T) {
	bad := `{"type":"array"}`
	messages := &CodeModeToolMessages{Wait: &CodeModeToolMessage{Parameters: &bad}}
	wait := (&codeModeWaitExecutor{exec: &codeModeExecExecutor{messages: messages}}).Spec()
	if wait.InputSchema["type"] != "object" || wait.InputSchema["required"] == nil {
		t.Fatalf("invalid schema did not fall back: %#v", wait.InputSchema)
	}
}
