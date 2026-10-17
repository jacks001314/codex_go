package context

import (
	"reflect"
	"testing"

	"codex_go/eventmap"
)

// TestToolOutputPartsPreservesReferencesLikeRust pins the shared
// content-to-tool-output conversion of Rust #54776: output text becomes input
// text, a file-backed image keeps its file id, an inline image keeps its URL and
// detail, and audio keeps its audio URL.
func TestToolOutputPartsPreservesReferencesLikeRust(t *testing.T) {
	parts := ToolOutputParts([]eventmap.ContentItem{
		{Kind: eventmap.ContentOutputText, Text: "caption"},
		{Kind: eventmap.ContentInputImage, ImageURL: "data:image/png;base64,AAAA", Detail: "high"},
		{Kind: eventmap.ContentInputImage, FileID: "file-1"},
		{Kind: eventmap.ContentInputAudio, AudioURL: "data:audio/wav;base64,AAAA"},
	})
	want := []map[string]any{
		{"type": "input_text", "text": "caption"},
		{"type": "input_image", "image_url": "data:image/png;base64,AAAA", "detail": "high"},
		{"type": "input_image", "file_id": "file-1"},
		{"type": "input_audio", "audio_url": "data:audio/wav;base64,AAAA"},
	}
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("ToolOutputParts() = %#v, want %#v", parts, want)
	}
}

// TestToolOutputTextMatchesCodeModeConversion mirrors Rust's
// content_items_to_code_mode_result: non-empty text parts join with newlines,
// images contribute their URL or file id, audio contributes its URL, and empty
// parts are skipped.
func TestToolOutputTextMatchesCodeModeConversion(t *testing.T) {
	parts := []map[string]any{
		{"type": "input_text", "text": "first"},
		{"type": "input_text", "text": "   "},
		{"type": "input_image", "file_id": "file-1"},
		{"type": "input_image", "image_url": "data:image/png;base64,AAAA"},
		{"type": "input_audio", "audio_url": "data:audio/wav;base64,AAAA"},
		{"type": "output_text", "text": "second"},
	}
	if got, want := ToolOutputText(parts), "first\nfile-1\ndata:image/png;base64,AAAA\ndata:audio/wav;base64,AAAA\nsecond"; got != want {
		t.Fatalf("ToolOutputText() = %q, want %q", got, want)
	}
	if got := ToolOutputText(nil); got != "" {
		t.Fatalf("ToolOutputText(nil) = %q, want empty", got)
	}
}
