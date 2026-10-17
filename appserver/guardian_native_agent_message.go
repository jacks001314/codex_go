package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/session"
)

// guardianNativeAgentMessage returns the complete stored agent_message item when
// it carries encrypted content (#49038). Rust keeps the native item so the
// reviewer receives author, recipient and ciphertext unchanged; rendering the
// ciphertext as transcript text would lose the encrypted part.
func guardianNativeAgentMessage(item *session.Item) (json.RawMessage, bool) {
	if item == nil {
		return nil, false
	}
	switch strings.TrimSpace(item.Type) {
	case "agent_message", "agentMessage":
	default:
		return nil, false
	}
	candidates := make([]any, 0, 3)
	if item.Metadata != nil {
		if raw, ok := item.Metadata["rawResponseItem"]; ok {
			candidates = append(candidates, raw)
		}
	}
	if len(item.Raw) > 0 {
		candidates = append(candidates, item.Raw)
	}
	if len(item.Data) > 0 {
		candidates = append(candidates, item.Data)
	}
	for _, candidate := range candidates {
		if payload, ok := guardianResponseItemWithEncryptedContent(candidate); ok {
			return payload, true
		}
	}
	return nil, false
}

// guardianResponseItemWithEncryptedContent serializes the stored payload when
// any content part is a non-empty encrypted_content part.
func guardianResponseItemWithEncryptedContent(value any) (json.RawMessage, bool) {
	var payload map[string]any
	switch typed := value.(type) {
	case map[string]any:
		payload = typed
	case json.RawMessage:
		if err := json.Unmarshal(typed, &payload); err != nil {
			return nil, false
		}
	case []byte:
		if err := json.Unmarshal(typed, &payload); err != nil {
			return nil, false
		}
	default:
		return nil, false
	}
	if payload == nil {
		return nil, false
	}
	parts, ok := payload["content"].([]any)
	if !ok {
		return nil, false
	}
	encrypted := false
	for _, part := range parts {
		typed, ok := part.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(stringFromAny(typed["type"])) != "encrypted_content" {
			continue
		}
		if strings.TrimSpace(stringFromAny(typed["encrypted_content"])) != "" {
			encrypted = true
			break
		}
	}
	if !encrypted {
		return nil, false
	}
	if strings.TrimSpace(stringFromAny(payload["type"])) == "" {
		payload["type"] = "agent_message"
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, false
	}
	return data, true
}
