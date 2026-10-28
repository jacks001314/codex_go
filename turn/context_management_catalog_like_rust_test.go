package turn

import (
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

func TestToolRegistryAppliesContextManagementCatalogMessagesLikeRust(t *testing.T) {
	newContextDescription := "catalog new context"
	remainingDescription := "catalog remaining"
	remainingParameters := `{"type":"object","properties":{"windows":{"type":"number"}}}`
	options := DefaultToolRegistryOptions(t.TempDir())
	options.NewContextWindow = func() {}
	options.ContextManagementMessages = &model.ContextManagementToolMessages{
		NewContext:          &model.ToolMessage{Description: &newContextDescription},
		GetContextRemaining: &model.ToolMessage{Description: &remainingDescription, Parameters: &remainingParameters},
	}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatalf("BuildToolRegistry() error = %v", err)
	}
	newContext, ok := registry.Spec(tool.PlainName("new_context"))
	if !ok || newContext.Description != newContextDescription {
		t.Fatalf("new_context spec = %#v, ok=%v", newContext, ok)
	}
	remaining, ok := registry.Spec(tool.PlainName("get_context_remaining"))
	if !ok || remaining.Description != remainingDescription {
		t.Fatalf("get_context_remaining spec = %#v, ok=%v", remaining, ok)
	}
	properties, _ := remaining.InputSchema["properties"].(map[string]any)
	if _, ok := properties["windows"]; !ok {
		t.Fatalf("get_context_remaining parameters were not overridden: %#v", remaining.InputSchema)
	}
}
