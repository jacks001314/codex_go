package appserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"codex_go/session"
	"codex_go/turn"
)

// TestUserInputImageReferencesKeepStableWireShapes mirrors Rust's
// `user_input_image_references_round_trip_with_stable_wire_shapes` and
// `file_image_user_input_converts_both_directions` (#45794): the URL form is
// unchanged, the uploaded-file form travels as `fileId` on the v2 surface and
// as `file_id` toward the Responses API, and both survive thread history.
func TestUserInputImageReferencesKeepStableWireShapes(t *testing.T) {
	detail := "high"
	inputs := []turn.TurnUserInput{
		{Type: "image", URL: "data:image/png;base64,AAA", Detail: &detail},
		{Type: "image", FileID: "file_123", Detail: &detail},
	}

	// Toward the v2 client (the in-flight user item and thread history).
	threadContent := threadUserInputContent("", inputs)
	wantThread := []map[string]any{
		{"type": "image", "url": "data:image/png;base64,AAA", "detail": "high"},
		{"type": "image", "fileId": "file_123", "detail": "high"},
	}
	if !reflect.DeepEqual(threadContent, wantThread) {
		t.Fatalf("threadUserInputContent() = %#v, want %#v", threadContent, wantThread)
	}

	// Toward the Responses API.
	apiContent := inputContentFromTurnUserInputs("", inputs)
	wantAPI := []map[string]any{
		{"type": "input_image", "image_url": "data:image/png;base64,AAA", "detail": "high"},
		{"type": "input_image", "file_id": "file_123", "detail": "high"},
	}
	if !reflect.DeepEqual(apiContent, wantAPI) {
		t.Fatalf("inputContentFromTurnUserInputs() = %#v, want %#v", apiContent, wantAPI)
	}

	// Into durable thread history.
	parts := sessionContentFromTurnUserInputs(inputs)
	want := []session.ContentPart{
		{Type: "image", ImageURL: "data:image/png;base64,AAA", Detail: &detail},
		{Type: "input_image", FileID: "file_123", Detail: &detail},
	}
	if !reflect.DeepEqual(parts, want) {
		t.Fatalf("sessionContentFromTurnUserInputs() = %#v, want %#v", parts, want)
	}

	// History read: the session part becomes a v2 item content entry with
	// `fileId`, and the response item payload round-trips it back.
	threadItemContent := threadItemContentFromSession(parts)
	encoded, err := json.Marshal(threadItemContent[1])
	if err != nil {
		t.Fatalf("Marshal(thread item content) error = %v", err)
	}
	if got := string(encoded); !strings.Contains(got, `"fileId":"file_123"`) || !strings.Contains(got, `"type":"input_image"`) {
		t.Fatalf("thread item content = %s", got)
	}
	rebuilt := sessionContentPartsFromResponseContent([]any{
		map[string]any{"type": "input_image", "file_id": "file_123", "detail": "high"},
		map[string]any{"type": "input_image", "fileId": "file_456"},
	}, "user")
	if len(rebuilt) != 2 {
		t.Fatalf("sessionContentPartsFromResponseContent() = %#v", rebuilt)
	}
	if rebuilt[0].FileID != "file_123" || rebuilt[0].ImageURL != "" {
		t.Fatalf("rebuilt[0] = %#v", rebuilt[0])
	}
	if rebuilt[1].FileID != "file_456" {
		t.Fatalf("rebuilt[1] = %#v", rebuilt[1])
	}
}

// TestSessionPartFileReferenceRendersAsFileID keeps the history projection of a
// stored file reference on the `fileId` form, matching the v2 schema.
func TestSessionPartFileReferenceRendersAsFileID(t *testing.T) {
	content := threadItemContentFromSession([]session.ContentPart{
		{Type: "input_image", FileID: "file_789"},
	})
	if len(content) != 1 || content[0].FileID != "file_789" || content[0].ImageURL != "" {
		t.Fatalf("threadItemContentFromSession() = %#v", content)
	}
	rendered := threadItemUserInputContent(&ThreadItem{Type: "userMessage", Content: content})
	want := []map[string]any{{"type": "image", "fileId": "file_789"}}
	if !reflect.DeepEqual(rendered, want) {
		t.Fatalf("threadItemUserInputContent() = %#v, want %#v", rendered, want)
	}
}

// TestTurnUserInputFileReferenceCountsAsImage keeps resize-notice numbering and
// the image budget consistent with Rust: an uploaded file counts as an image
// even though it is never resolved locally.
func TestTurnUserInputFileReferenceCountsAsImage(t *testing.T) {
	inputs := []turn.TurnUserInput{
		{Type: "image", FileID: "file_1"},
		{Type: "image", URL: "data:image/png;base64,AAA"},
		{Type: "text", Text: "hello"},
	}
	if got := countTurnUserInputImages(inputs); got != 2 {
		t.Fatalf("countTurnUserInputImages() = %d, want 2", got)
	}
	// A file reference carries no URL, so the remote-URL guard leaves it alone.
	if err := validateTurnUserInputImageURLs(inputs); err != nil {
		t.Fatalf("validateTurnUserInputImageURLs() error = %v", err)
	}
}
