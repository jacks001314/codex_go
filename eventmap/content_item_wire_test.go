package eventmap

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestContentItemWireMatchesRustShape pins Rust #54776's shared content shape:
// the `type` tag plus the reference the item actually carries (image_url or
// file_id), with detail omitted when unset.
func TestContentItemWireMatchesRustShape(t *testing.T) {
	for _, testCase := range []struct {
		name string
		item ContentItem
		want map[string]any
	}{
		{
			name: "input text",
			item: ContentItem{Kind: ContentInputText, Text: "hello"},
			want: map[string]any{"type": "input_text", "text": "hello"},
		},
		{
			name: "output text",
			item: ContentItem{Kind: ContentOutputText, Text: "hello"},
			want: map[string]any{"type": "output_text", "text": "hello"},
		},
		{
			name: "inline image with detail",
			item: ContentItem{Kind: ContentInputImage, ImageURL: "data:image/png;base64,AAAA", Detail: "high"},
			want: map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAAA", "detail": "high"},
		},
		{
			name: "file image",
			item: ContentItem{Kind: ContentInputImage, FileID: "file-1"},
			want: map[string]any{"type": "input_image", "file_id": "file-1"},
		},
		{
			name: "file image wins over an inline url",
			item: ContentItem{Kind: ContentInputImage, ImageURL: "data:image/png;base64,AAAA", FileID: "file-1"},
			want: map[string]any{"type": "input_image", "file_id": "file-1"},
		},
		{
			name: "audio",
			item: ContentItem{Kind: ContentInputAudio, AudioURL: "data:audio/wav;base64,AAAA"},
			want: map[string]any{"type": "input_audio", "audio_url": "data:audio/wav;base64,AAAA"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.item.Wire(); !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("Wire() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

// TestContentItemRustJSONMatchesSerde pins the exact bytes Rust's serde emits,
// because the world-state hash covers them (#54776).
func TestContentItemRustJSONMatchesSerde(t *testing.T) {
	for _, testCase := range []struct {
		name string
		item ContentItem
		want string
	}{
		{name: "input text", item: ContentItem{Kind: ContentInputText, Text: "hi"}, want: `{"type":"input_text","text":"hi"}`},
		{name: "output text", item: ContentItem{Kind: ContentOutputText, Text: "hi"}, want: `{"type":"output_text","text":"hi"}`},
		{
			name: "inline image",
			item: ContentItem{Kind: ContentInputImage, ImageURL: "data:image/png;base64,AAAA", Detail: "original"},
			want: `{"type":"input_image","image_url":"data:image/png;base64,AAAA","detail":"original"}`,
		},
		{
			name: "file image without detail",
			item: ContentItem{Kind: ContentInputImage, FileID: "extension-image"},
			want: `{"type":"input_image","file_id":"extension-image"}`,
		},
		{
			name: "audio",
			item: ContentItem{Kind: ContentInputAudio, AudioURL: "audio-one"},
			want: `{"type":"input_audio","audio_url":"audio-one"}`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			data, err := testCase.item.RustJSON()
			if err != nil {
				t.Fatalf("RustJSON() error = %v", err)
			}
			if string(data) != testCase.want {
				t.Fatalf("RustJSON() = %s, want %s", data, testCase.want)
			}
		})
	}
}

// TestContentItemsRustJSONSingleVersusArray pins the hash-input shape: one item
// serializes as the item (Rust fragments render one), several as an array.
func TestContentItemsRustJSONSingleVersusArray(t *testing.T) {
	single := []ContentItem{{Kind: ContentInputAudio, AudioURL: "audio-one"}}
	if got, want := string(ContentItemsRustJSON(single)), `{"type":"input_audio","audio_url":"audio-one"}`; got != want {
		t.Fatalf("single = %s, want %s", got, want)
	}
	multiple := []ContentItem{
		{Kind: ContentInputText, Text: "caption"},
		{Kind: ContentInputImage, FileID: "extension-image"},
	}
	var decoded []map[string]any
	if err := json.Unmarshal(ContentItemsRustJSON(multiple), &decoded); err != nil {
		t.Fatalf("multiple: %v", err)
	}
	if len(decoded) != 2 || decoded[1]["file_id"] != "extension-image" {
		t.Fatalf("multiple = %#v", decoded)
	}
}
