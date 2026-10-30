package codexapi

import (
	"encoding/json"
	"testing"
)

// Rust #47956: an image edit request accepts mixed inline and file references,
// serializing each with exactly one of image_url or file_id.
func TestImageEditRequestSerializesMixedImageReferencesLikeRust(t *testing.T) {
	request := ImageEditRequest{
		Images: []ImageReference{
			{ImageURL: "data:image/png;base64,Zm9v"},
			{FileID: "file-image"},
		},
		Prompt: "add a red hat",
		Model:  "gpt-image-1.5",
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	want := `{"images":[{"image_url":"data:image/png;base64,Zm9v"},{"file_id":"file-image"}],"prompt":"add a red hat","model":"gpt-image-1.5"}`
	if string(encoded) != want {
		t.Fatalf("request body = %s, want %s", encoded, want)
	}
}
