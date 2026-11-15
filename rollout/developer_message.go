package rollout

import (
	"encoding/json"
	"strings"

	"codex_go/session"
)

// developerMessageRolloutItem keeps a developer message in its ResponseItem
// form. Rust stores injected developer policy as response_item records; the
// paginated TurnItem vocabulary only has UserMessage, so collapsing it would
// lose the role that saved side-chat discovery checks (Rust
// side_context_checkpoint).
func developerMessageRolloutItem(item *session.Item) (json.RawMessage, json.RawMessage, bool) {
	if item == nil || !strings.EqualFold(strings.TrimSpace(item.Role), "developer") {
		return nil, nil, false
	}
	itemType := strings.ToLower(strings.TrimSpace(item.Type))
	if itemType != "" && itemType != "message" {
		return nil, nil, false
	}
	payload := developerResponseItemPayload(item)
	if len(payload) == 0 {
		return nil, nil, false
	}
	return payload, harnessMetadataRaw(item), true
}

func developerResponseItemPayload(item *session.Item) json.RawMessage {
	if item == nil {
		return nil
	}
	if raw, ok := item.Metadata["rawResponseItem"].(map[string]any); ok {
		if strings.EqualFold(strings.TrimSpace(stringFromMap(raw, "role")), "developer") {
			if encoded, err := json.Marshal(raw); err == nil {
				return encoded
			}
		}
	}
	content := make([]map[string]any, 0, len(item.Content)+1)
	for _, part := range item.Content {
		switch strings.ToLower(strings.TrimSpace(part.Type)) {
		case "input_text", "text", "output_text":
			content = append(content, map[string]any{"type": "input_text", "text": part.Text})
		case "input_image", "image":
			if strings.TrimSpace(part.ImageURL) != "" {
				content = append(content, map[string]any{"type": "input_image", "image_url": part.ImageURL})
			}
		}
	}
	if len(content) == 0 {
		content = append(content, map[string]any{"type": "input_text", "text": item.Text})
	}
	encoded, err := json.Marshal(map[string]any{
		"type":    "message",
		"role":    "developer",
		"content": content,
	})
	if err != nil {
		return nil
	}
	return encoded
}

func harnessMetadataRaw(item *session.Item) json.RawMessage {
	if item == nil {
		return nil
	}
	switch value := item.Data["harness_metadata"].(type) {
	case json.RawMessage:
		if len(value) > 0 && string(value) != "null" {
			return value
		}
	case string:
		if strings.TrimSpace(value) != "" {
			return json.RawMessage(value)
		}
	}
	return nil
}
