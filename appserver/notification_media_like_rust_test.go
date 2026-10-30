package appserver

import (
	"os"
	"testing"

	"codex_go/config"
)

// TestNotificationMediaFilterStripsImageGenerationAndMcpBlobsLikeRust mirrors
// Rust #41416 notification_media.rs: image-generation results lose their inline
// base64 payload while keeping the file reference and prompts, and MCP tool
// results drop image/audio and binary resource-blob entries.
func TestNotificationMediaFilterStripsImageGenerationAndMcpBlobsLikeRust(t *testing.T) {
	payload := ThreadItemPayload{
		"type":          "imageGeneration",
		"id":            "call-image",
		"result":        "aW5saW5l",
		"revisedPrompt": "draw a square",
		"inputImages":   []any{map[string]any{"path": "/tmp/a.png", "sha256": "abc"}},
	}
	started := withoutNotificationMedia(&ItemStartedNotification{Item: payload}).(*ItemStartedNotification)
	if _, ok := started.Item["result"]; ok {
		t.Fatalf("image generation result = %#v, want it cleared", started.Item["result"])
	}
	if started.Item["revisedPrompt"] != "draw a square" {
		t.Fatalf("revisedPrompt = %#v", started.Item["revisedPrompt"])
	}
	if images, ok := started.Item["inputImages"].([]any); !ok || len(images) != 1 {
		t.Fatalf("inputImages = %#v, want one entry", started.Item["inputImages"])
	}

	raw := withoutNotificationMedia(&RawResponseItemCompletedNotification{Item: map[string]any{
		"type":   "image_generation_call",
		"id":     "ig",
		"result": "base64",
		"status": "completed",
	}}).(*RawResponseItemCompletedNotification)
	rawItem := raw.Item.(map[string]any)
	if _, ok := rawItem["result"]; ok {
		t.Fatalf("raw image generation result = %#v, want it cleared", rawItem["result"])
	}
	if rawItem["status"] != "completed" {
		t.Fatalf("raw item = %#v", rawItem)
	}

	mcp := ThreadItemPayload{
		"type": "mcpToolCall",
		"result": map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "ok"},
			map[string]any{"type": "image", "data": "aW1n"},
			map[string]any{"type": "resource", "resource": map[string]any{"uri": "file:///a.png", "blob": "YmxvYg=="}},
			map[string]any{"type": "resource", "resource": map[string]any{"uri": "file:///b.md", "text": "hi"}},
		}},
	}
	completed := withoutNotificationMedia(&ItemCompletedNotification{Item: mcp}).(*ItemCompletedNotification)
	content := completed.Item["result"].(map[string]any)["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("mcp content = %#v, want text plus the blob-less resource", content)
	}
	if content[0].(map[string]any)["type"] != "text" {
		t.Fatalf("mcp content[0] = %#v", content[0])
	}
	resource := content[1].(map[string]any)["resource"].(map[string]any)
	if resource["uri"] != "file:///b.md" {
		t.Fatalf("mcp content[1] = %#v", content[1])
	}
}

// TestNotificationMediaFilterKeepsLocalMediaLikeRust mirrors Rust's
// UserInput retention rules: inline image/audio content is removed while local
// file references stay.
func TestNotificationMediaFilterKeepsLocalMediaLikeRust(t *testing.T) {
	payload := ThreadItemPayload{
		"type": "userMessage",
		"content": []any{
			map[string]any{"type": "text", "text": "hi"},
			map[string]any{"type": "image", "url": "data:image/png;base64,xyz"},
			map[string]any{"type": "local_image", "path": "/tmp/a.png"},
			map[string]any{"type": "audio", "url": "data:audio/wav;base64,abc"},
			map[string]any{"type": "local_audio", "path": "/tmp/a.wav"},
		},
	}
	completed := withoutNotificationMedia(&ItemCompletedNotification{Item: payload}).(*ItemCompletedNotification)
	content := completed.Item["content"].([]any)
	if len(content) != 3 {
		t.Fatalf("content = %#v, want text, local_image and local_audio", content)
	}
	for i, want := range []string{"text", "local_image", "local_audio"} {
		if got := content[i].(map[string]any)["type"]; got != want {
			t.Fatalf("content[%d] = %#v, want %s", i, content[i], want)
		}
	}
}

// TestNotificationMediaFilterGateLikeRust covers the disabled-by-default
// `omit_app_server_notification_media` feature across the three filtered
// notification types.
func TestNotificationMediaFilterGateLikeRust(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)

	router.notify(NotificationItemStarted, itemStartedWithInlineImage())
	started := lastNotificationParams(t, sink).(*ItemStartedNotification)
	if !threadItemPayloadHasInlineImage(started.Item) {
		t.Fatalf("feature disabled: media was filtered out of item/started")
	}

	if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nomit_app_server_notification_media = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	router.notify(NotificationItemStarted, itemStartedWithInlineImage())
	started = lastNotificationParams(t, sink).(*ItemStartedNotification)
	if threadItemPayloadHasInlineImage(started.Item) {
		t.Fatalf("feature enabled: item/started kept inline media: %#v", started.Item)
	}

	router.notify(NotificationItemCompleted, &ItemCompletedNotification{ThreadID: "thread-1", TurnID: "turn-1", Item: ThreadItemPayload{
		"type": "userMessage",
		"content": []any{
			map[string]any{"type": "text", "text": "hi"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,xyz"},
		},
	}})
	completed := lastNotificationParams(t, sink).(*ItemCompletedNotification)
	if threadItemPayloadHasInlineImage(completed.Item) {
		t.Fatalf("feature enabled: item/completed kept inline media: %#v", completed.Item)
	}

	router.notify(NotificationRawResponseItemCompleted, &RawResponseItemCompletedNotification{ThreadID: "thread-1", TurnID: "turn-1", Item: map[string]any{
		"type": "message",
		"content": []any{
			map[string]any{"type": "input_text", "text": "hi"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,xyz"},
		},
	}})
	raw := lastNotificationParams(t, sink).(*RawResponseItemCompletedNotification)
	content := raw.Item.(map[string]any)["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("feature enabled: rawResponseItem/completed content = %#v", content)
	}
}

func itemStartedWithInlineImage() *ItemStartedNotification {
	return &ItemStartedNotification{ThreadID: "thread-1", TurnID: "turn-1", Item: ThreadItemPayload{
		"type": "userMessage",
		"content": []any{
			map[string]any{"type": "text", "text": "hi"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,xyz"},
		},
	}}
}

func lastNotificationParams(t *testing.T, sink *NotificationBuffer) any {
	t.Helper()
	listed := sink.List()
	if len(listed) == 0 {
		t.Fatalf("no notification reached the sink")
	}
	return listed[len(listed)-1].Params
}

func threadItemPayloadHasInlineImage(payload ThreadItemPayload) bool {
	switch content := payload["content"].(type) {
	case []any:
		for _, entry := range content {
			if item, ok := entry.(map[string]any); ok && isMediaContentItemType(stringFromAny(item["type"])) {
				return true
			}
		}
	default:
		_, ok := payload["image_url"]
		return ok
	}
	return false
}
