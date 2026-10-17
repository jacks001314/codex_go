package tool

import "testing"

// TestCodeModeToolResultUsesTypedContentLikeRust mirrors the Rust #54776
// code-mode output conversion: a typed tool result contributes its text, image
// reference and audio URL to `output` instead of an empty string.
func TestCodeModeToolResultUsesTypedContentLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name string
		data map[string]any
		want string
	}{
		{
			name: "text and image",
			data: map[string]any{"content_items": []map[string]any{
				{"type": "input_text", "text": "caption"},
				{"type": "input_image", "file_id": "file-1"},
			}},
			want: "caption\nfile-1",
		},
		{
			name: "inline image",
			data: map[string]any{"content_items": []map[string]any{
				{"type": "input_image", "image_url": "data:image/png;base64,AAAA"},
			}},
			want: "data:image/png;base64,AAAA",
		},
		{
			name: "audio",
			data: map[string]any{"content_items": []map[string]any{
				{"type": "input_audio", "audio_url": "data:audio/wav;base64,AAAA"},
			}},
			want: "data:audio/wav;base64,AAAA",
		},
		{
			name: "dynamic tool any slice",
			data: map[string]any{"content_items": []any{
				map[string]any{"type": "input_image", "file_id": "file-2"},
			}},
			want: "file-2",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result := codeModeToolResult(&Output{Success: true, Data: testCase.data})
			if result["output"] != testCase.want {
				t.Fatalf("output = %#v, want %q", result["output"], testCase.want)
			}
		})
	}

	// A textual body stays authoritative, matching Rust's text body branch.
	result := codeModeToolResult(&Output{
		Success: true,
		Body:    "plain body",
		Data:    map[string]any{"content_items": []map[string]any{{"type": "input_image", "file_id": "file-1"}}},
	})
	if result["output"] != "plain body" {
		t.Fatalf("output = %#v, want the text body", result["output"])
	}
}
