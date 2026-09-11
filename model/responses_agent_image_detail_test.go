package model

import "testing"

// Mirrors Rust #44249: image detail is normalized on request copies for the
// receiving model, covering user messages and tool outputs, without mutating
// the stored items.
func TestNormalizeResponseInputImageDetailsForModel(t *testing.T) {
	items := []any{
		map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "hi"},
				map[string]any{"type": "input_image", "image_url": "data:", "detail": "original"},
			},
		},
		map[string]any{
			"type":    "function_call_output",
			"call_id": "call-1",
			"output": []any{
				map[string]any{"type": "input_image", "image_url": "data:", "detail": "original"},
				map[string]any{"type": "input_image", "image_url": "data:", "detail": "low"},
			},
		},
	}

	got := normalizeResponseInputImageDetailsForModel(items, false)
	messageImage := got[0].(map[string]any)["content"].([]any)[1].(map[string]any)
	if messageImage["detail"] != "high" {
		t.Fatalf("message image detail = %v, want high", messageImage["detail"])
	}
	toolOutput := got[1].(map[string]any)["output"].([]any)
	if toolOutput[0].(map[string]any)["detail"] != "high" {
		t.Fatalf("tool output image detail = %v, want high", toolOutput[0].(map[string]any)["detail"])
	}
	if toolOutput[1].(map[string]any)["detail"] != "low" {
		t.Fatalf("low tool output image detail = %v, want low", toolOutput[1].(map[string]any)["detail"])
	}
	if items[0].(map[string]any)["content"].([]any)[1].(map[string]any)["detail"] != "original" {
		t.Fatal("stored message image detail was mutated")
	}
	if items[1].(map[string]any)["output"].([]any)[0].(map[string]any)["detail"] != "original" {
		t.Fatal("stored tool output image detail was mutated")
	}

	kept := normalizeResponseInputImageDetailsForModel(items, true)
	if kept[0].(map[string]any)["content"].([]any)[1].(map[string]any)["detail"] != "original" {
		t.Fatal("supporting model must keep original image detail")
	}
}
