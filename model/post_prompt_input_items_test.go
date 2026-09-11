package model

import "testing"

func TestResponsesInputItemsAppendPostPromptItemsAfterPrompt(t *testing.T) {
	history := map[string]any{
		"type":    "message",
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": "earlier"}},
	}
	update := map[string]any{
		"type":      "configuration_update",
		"reasoning": map[string]any{"effort": "high"},
	}

	items := responsesInputItems(&AgentRequest{
		Prompt:               "hello",
		InputItems:           []any{history},
		PostPromptInputItems: []any{update},
	})
	if len(items) != 3 {
		t.Fatalf("items = %#v, want history + prompt + post-prompt", items)
	}
	if got, ok := items[0].(map[string]any); !ok || got["type"] != "message" {
		t.Fatalf("items[0] = %#v, want history message", items[0])
	}
	prompt, ok := items[1].(responsesInputMessage)
	if !ok || prompt.Role != "user" || len(prompt.Content) != 1 || prompt.Content[0].Text != "hello" {
		t.Fatalf("items[1] = %#v, want prompt user message", items[1])
	}
	if got, ok := items[2].(map[string]any); !ok || got["type"] != "configuration_update" {
		t.Fatalf("items[2] = %#v, want post-prompt configuration update", items[2])
	}
}

func TestResponsesInputItemsAppendPostPromptItemsWithoutPrompt(t *testing.T) {
	history := map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "on"}}}
	update := map[string]any{"type": "configuration_update", "reasoning": map[string]any{"effort": "high"}}
	items := responsesInputItems(&AgentRequest{InputItems: []any{history}, PostPromptInputItems: []any{update}})
	if len(items) != 2 {
		t.Fatalf("items = %#v, want history + post-prompt", items)
	}
	if got, ok := items[1].(map[string]any); !ok || got["type"] != "configuration_update" {
		t.Fatalf("items[1] = %#v, want configuration update last", items[1])
	}
}
