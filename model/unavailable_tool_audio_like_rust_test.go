package model

import "testing"

// TestResponsesInputReplacesUnavailableToolAudioLikeRust mirrors Rust #61394
// media_availability: a file-backed tool clip the store confirms missing is
// projected as a placeholder, while user message audio and the retained history
// keep their original references.
func TestResponsesInputReplacesUnavailableToolAudioLikeRust(t *testing.T) {
	request := &AgentRequest{
		Store: true,
		InputItems: []any{
			map[string]any{
				"type": "message",
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_audio", "file_id": "file-user-audio"},
				},
			},
			map[string]any{
				"type":    "function_call_output",
				"call_id": "call-clip",
				"output": []any{
					map[string]any{"type": "input_text", "text": "before"},
					map[string]any{"type": "input_audio", "file_id": "file-missing-tool"},
					map[string]any{"type": "input_audio", "file_id": "file-present-tool"},
					map[string]any{"type": "input_text", "text": "after"},
				},
			},
		},
	}
	store := &resolvableImageStore{missing: map[string]bool{
		"file-missing-tool": true,
		"file-user-audio":   true,
	}}
	items := responsesInputItemsWithStore(request, store)
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}

	message, _ := items[0].(map[string]any)
	messageContent, _ := message["content"].([]any)
	userAudio, _ := messageContent[0].(map[string]any)
	if userAudio["type"] != "input_audio" || userAudio["file_id"] != "file-user-audio" {
		t.Fatalf("user message audio changed: %#v", messageContent)
	}

	outputItem, _ := items[1].(map[string]any)
	output, _ := outputItem["output"].([]any)
	placeholder, _ := output[1].(map[string]any)
	if placeholder["type"] != "input_text" || placeholder["text"] != UnavailableToolAudioPlaceholder {
		t.Fatalf("tool audio placeholder = %#v", output[1])
	}
	present, _ := output[2].(map[string]any)
	if present["type"] != "input_audio" || present["file_id"] != "file-present-tool" {
		t.Fatalf("available tool audio changed: %#v", output[2])
	}
	after, _ := output[3].(map[string]any)
	if after["type"] != "input_text" || after["text"] != "after" {
		t.Fatalf("following text changed: %#v", output[3])
	}

	originalOutputItem, _ := request.InputItems[1].(map[string]any)
	originalOutput, _ := originalOutputItem["output"].([]any)
	originalClip, _ := originalOutput[1].(map[string]any)
	if originalClip["file_id"] != "file-missing-tool" {
		t.Fatalf("retained history changed: %#v", request.InputItems[1])
	}
}
