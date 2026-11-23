package appserver

import (
	"testing"

	"codex_go/session"
)

// Mirrors Rust #63364 (guardian-v3 evidence.rs::without_image_attachments):
// evidence capture keeps the caption text around an inline image, drops an
// image-only message, and still includes later text.
func TestGuardianEvidenceExcludesUserImagesLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	threadID := "thread-images"
	items := []session.Item{
		{
			ID: "user-with-caption", Type: "message", Role: "user",
			Content: []session.ContentPart{
				{Type: "input_text", Text: "look at this screenshot"},
				{Type: "input_text", Text: "<image>"},
				{Type: "input_image", ImageURL: "data:image/png;base64,AAAA"},
				{Type: "input_text", Text: "</image>"},
				{Type: "input_text", Text: "what is wrong here?"},
			},
		},
		{
			ID: "user-image-only", Type: "message", Role: "user",
			Content: []session.ContentPart{
				{Type: "input_image", ImageURL: "data:image/png;base64,BBBB"},
			},
		},
		{
			ID: "user-image-only-labels", Type: "message", Role: "user",
			Content: []session.ContentPart{
				{Type: "input_text", Text: "<image name=screen.png>"},
				{Type: "input_image", ImageURL: "data:image/png;base64,CCCC"},
				{Type: "input_text", Text: "</image>"},
			},
		},
		{
			ID: "assistant-note", Type: "message", Role: "assistant",
			Content: []session.ContentPart{
				{Type: "input_text", Text: "done"},
			},
		},
	}
	filtered := router.filterGuardianReviewHistoryItems(threadID, items)
	if len(filtered) != 2 {
		t.Fatalf("filtered evidence = %#v, want the captioned message and the later text", filtered)
	}
	if filtered[0].ID != "user-with-caption" {
		t.Fatalf("first evidence item = %q", filtered[0].ID)
	}
	for _, part := range filtered[0].Content {
		if guardianContentPartIsImage(part) || guardianIsImageOpenLabel(part) || guardianIsImageCloseLabel(part) {
			t.Fatalf("image or its labels survived: %#v", filtered[0].Content)
		}
	}
	if len(filtered[0].Content) != 2 || filtered[0].Content[1].Text != "what is wrong here?" {
		t.Fatalf("caption text = %#v", filtered[0].Content)
	}
	if filtered[1].ID != "assistant-note" {
		t.Fatalf("later text was not included: %#v", filtered)
	}
}
