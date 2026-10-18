package turn

import (
	"testing"

	"codex_go/model"
	"codex_go/tool"
)

func TestToolRegistryAppliesCodeModeCatalogMessagesLikeRust(t *testing.T) {
	waitText := "Registry wait"
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCodeMode = true
	options.ToolMode = model.ToolModeCodeModeOnly
	options.CodeModeMessages = &model.CodeModeToolMessages{
		Wait: &model.CodeModeToolMessage{Description: &waitText},
	}
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	wait, ok := registry.Spec(tool.PlainName(tool.CodeModeWaitToolName))
	if !ok || wait.Description != waitText {
		t.Fatalf("wait spec = %#v, ok=%v", wait, ok)
	}
}

func TestRuntimeRefreshesCodeModeMessagesForModelLikeRust(t *testing.T) {
	firstWait := "First wait"
	secondWait := "Second wait"
	options := DefaultToolRegistryOptions(t.TempDir())
	options.EnableCodeMode = true
	options.ToolMode = model.ToolModeCodeModeOnly
	registry, err := BuildToolRegistry(options)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		router: tool.NewRouter(registry),
		codeModeMessagesForModel: func(modelID string) *model.CodeModeToolMessages {
			text := firstWait
			if modelID == "second" {
				text = secondWait
			}
			return &model.CodeModeToolMessages{Wait: &model.CodeModeToolMessage{Description: &text}}
		},
	}
	runtime.applyCodeModeMessagesForModel("second")
	var got string
	for _, spec := range runtime.modelVisibleSpecsForRequest(model.ToolModeCodeModeOnly) {
		if spec.Name.Key() == tool.CodeModeWaitToolName {
			got = spec.Description
		}
	}
	if got != secondWait {
		t.Fatalf("wait description = %q", got)
	}
}
