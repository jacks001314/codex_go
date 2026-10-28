package tool

import (
	"strings"
	"testing"
)

// Rust #57584 unified code-mode truncation: mixed output budgets text in input
// order, keeps images, and reports omitted text without dropping the media.
func TestTruncateCodeModeOutputPreservesMediaLikeRust(t *testing.T) {
	output := &Output{
		Success: true,
		Body:    strings.Repeat("a", 64) + "\n" + strings.Repeat("b", 64),
		Data: map[string]any{"content_items": []map[string]any{
			{"type": "input_text", "text": strings.Repeat("a", 64)},
			{"type": "input_image", "image_url": "data:image/png;base64,AAAA", "detail": "high"},
			{"type": "input_text", "text": strings.Repeat("b", 64)},
		}},
	}
	truncated := truncateCodeModeOutput(output, 1)
	items, ok := truncated.Data["content_items"].([]map[string]any)
	if !ok || len(items) != 3 {
		t.Fatalf("content items = %#v", truncated.Data["content_items"])
	}
	if items[0]["type"] != "input_text" || !strings.Contains(items[0]["text"].(string), "tokens truncated") {
		t.Fatalf("first item = %#v", items[0])
	}
	if items[1]["type"] != "input_image" || items[1]["image_url"] != "data:image/png;base64,AAAA" || items[1]["detail"] != "high" {
		t.Fatalf("image item = %#v", items[1])
	}
	if items[2]["type"] != "input_text" || items[2]["text"] != "[omitted 1 text items ...]" {
		t.Fatalf("omission item = %#v", items[2])
	}
	wantBody := items[0]["text"].(string) + "\n" + "[omitted 1 text items ...]"
	if truncated.Body != wantBody {
		t.Fatalf("body = %q, want %q", truncated.Body, wantBody)
	}
}

func TestTruncateCodeModeOutputKeepsUnchangedItemsLikeRust(t *testing.T) {
	items := []map[string]any{
		{"type": "input_text", "text": "short"},
		{"type": "input_image", "image_url": "data:image/png;base64,AAAA"},
	}
	output := &Output{Success: true, Body: "short", Data: map[string]any{"content_items": items}}
	truncated := truncateCodeModeOutput(output, 1000)
	got, ok := truncated.Data["content_items"].([]map[string]any)
	if !ok || len(got) != 2 || got[0]["text"] != "short" || got[1]["image_url"] != "data:image/png;base64,AAAA" {
		t.Fatalf("content items = %#v", truncated.Data["content_items"])
	}
	if truncated.Body != "short" {
		t.Fatalf("body = %q", truncated.Body)
	}
}
