package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Mirrors Rust #60270: a ChatGPT-authenticated OpenAI session warns once when
// the selected model is absent from the server catalog.
func TestCatalogWarnsWhenChatGPTModelIsMissingLikeRust(t *testing.T) {
	model := NewModel(&codextui.State{
		Model: "gpt-typo", Provider: "openai", HasChatGPTAccount: true,
	}, Options{Width: 100, Height: 30, HasChatGPTAccount: true})
	model.nextModelCatalogRequestID = 1
	model.pendingModelCatalogRequestID = 1
	model.applyModelCatalogResult(ModelCatalogResultMsg{
		RequestID: 1,
		Options:   []codextui.ModelPickerOption{{ID: "gpt-5", Label: "GPT-5", ShowInPicker: true}},
	})
	found := false
	for _, message := range model.State.Messages {
		if strings.Contains(message.Text, "Your ChatGPT model list doesn't include `gpt-typo`") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing model warning was not added: %#v", model.State.Messages)
	}
	// A second catalog response does not duplicate the warning.
	model.nextModelCatalogRequestID = 2
	model.pendingModelCatalogRequestID = 2
	model.applyModelCatalogResult(ModelCatalogResultMsg{
		RequestID: 2,
		Options:   []codextui.ModelPickerOption{{ID: "gpt-5", Label: "GPT-5", ShowInPicker: true}},
	})
	count := 0
	for _, message := range model.State.Messages {
		if strings.Contains(message.Text, "Your ChatGPT model list doesn't include") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("model warning count = %d, want 1", count)
	}
}
