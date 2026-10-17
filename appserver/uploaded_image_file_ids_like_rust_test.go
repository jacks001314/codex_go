package appserver

import (
	"testing"
	"time"

	"codex_go/turn"
)

// Mirrors Rust #46258: image preparation preserves uploaded file references,
// and the live/persisted user message display history carries the prepared
// file IDs instead of dropping them.
func TestUploadedImageFileIDsReachUserMessageDisplayHistoryLikeRust(t *testing.T) {
	placeholder := "file"
	params := &turn.TurnStartParams{
		Prompt: "inspect this",
		Input: []turn.TurnUserInput{
			{
				Type:         "image",
				FileID:       "file-uploaded-1",
				TextElements: nil,
			},
			{
				Type: "text",
				Text: "the attached report",
				TextElements: []turn.TextElement{{
					ByteRange:   turn.ByteRange{Start: 0, End: 6},
					Placeholder: &placeholder,
				}},
			},
		},
	}
	router := NewRuntimeRouter(RuntimeServices{})
	item, _ := router.preparedUserMessageInputItem("turn-uploaded", params)
	if item == nil {
		t.Fatal("prepared user message is nil")
	}
	content := item.(map[string]any)["content"].([]map[string]any)
	if len(content) != 3 {
		t.Fatalf("prepared content = %#v, want prompt, image, text", content)
	}
	if got := firstNonEmpty(stringFromAny(content[1]["file_id"]), stringFromAny(content[1]["fileId"])); got != "file-uploaded-1" {
		t.Fatalf("prepared image file id = %q, want file-uploaded-1", got)
	}
	if got := stringFromAny(content[2]["text"]); got != "the attached report" {
		t.Fatalf("prepared text = %q, want the original body", got)
	}

	sessionItem, ok := router.runtimeUserPromptSessionItem("turn-uploaded", params, time.Now().UTC())
	if !ok {
		t.Fatal("runtimeUserPromptSessionItem() did not produce the user message")
	}
	var sessionFileID string
	for index := range sessionItem.Content {
		if sessionItem.Content[index].FileID != "" {
			sessionFileID = sessionItem.Content[index].FileID
		}
	}
	if sessionFileID != "file-uploaded-1" {
		t.Fatalf("session content = %#v, want the uploaded file id", sessionItem.Content)
	}
	dataContent, _ := sessionItem.Data["content"].([]map[string]any)
	var dataFileID string
	var dataElements []turn.TextElement
	for _, block := range dataContent {
		if fileID := firstNonEmpty(stringFromAny(block["fileId"]), stringFromAny(block["file_id"])); fileID != "" {
			dataFileID = fileID
		}
		if elements, ok := block["text_elements"].([]turn.TextElement); ok {
			dataElements = elements
		}
	}
	if dataFileID != "file-uploaded-1" {
		t.Fatalf("thread item display content = %#v, want the uploaded file id", dataContent)
	}
	if len(dataElements) != 1 || dataElements[0].ByteRange.Start != 0 || dataElements[0].ByteRange.End != 6 {
		t.Fatalf("display text elements = %#v, want the original span preserved", dataElements)
	}
}
