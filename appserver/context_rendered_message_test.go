package appserver

import (
	"reflect"
	"testing"

	"codex_go/context"
	"codex_go/eventmap"
)

func TestBuildRenderedMessagePreservesTypedContentAndKindsLikeRust(t *testing.T) {
	rendered := &context.RenderedFragment{
		Role:        context.RoleUser,
		Content:     "hello",
		ContentKind: "test.context",
		ContentItems: []eventmap.ContentItem{
			{Kind: eventmap.ContentInputText, Text: "hello"},
			{Kind: eventmap.ContentInputImage, ImageURL: "data:image/png;base64,AAA"},
			{Kind: eventmap.ContentInputAudio, AudioURL: "data:audio/wav;base64,YXVkaW8="},
		},
	}
	item, ok := buildRenderedMessage(rendered).(map[string]any)
	if !ok {
		t.Fatalf("item type = %T", buildRenderedMessage(rendered))
	}
	content, ok := item["content"].([]map[string]any)
	if !ok || len(content) != 3 {
		t.Fatalf("content = %#v", item["content"])
	}
	if content[0]["type"] != "input_text" || content[0]["text"] != "hello" ||
		content[1]["type"] != "input_image" || content[1]["image_url"] != "data:image/png;base64,AAA" ||
		content[2]["type"] != "input_audio" || content[2]["audio_url"] != "data:audio/wav;base64,YXVkaW8=" {
		t.Fatalf("content = %#v", content)
	}
	metadata, ok := item["internal_chat_message_metadata_passthrough"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %#v", item["internal_chat_message_metadata_passthrough"])
	}
	kinds, ok := metadata["content_item_kinds"].([]string)
	if !ok || !reflect.DeepEqual(kinds, []string{"test.context", "test.context", "test.context"}) {
		t.Fatalf("content_item_kinds = %#v", metadata["content_item_kinds"])
	}
}

func TestBuildRenderedMessageAcceptsTypedContentWithoutTextFallback(t *testing.T) {
	rendered := &context.RenderedFragment{
		Role:        context.RoleUser,
		ContentKind: "test.image",
		ContentItems: []eventmap.ContentItem{{
			Kind:     eventmap.ContentInputImage,
			ImageURL: "data:image/png;base64,AAA",
		}},
	}
	item, ok := buildRenderedMessage(rendered).(map[string]any)
	if !ok {
		t.Fatalf("item type = %T", buildRenderedMessage(rendered))
	}
	content, ok := item["content"].([]map[string]any)
	if !ok || len(content) != 1 || content[0]["type"] != "input_image" {
		t.Fatalf("content = %#v", item["content"])
	}
}
