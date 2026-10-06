package rollout

import (
	"encoding/json"
	"strings"

	"codex_go/session"
)

// additionalToolsRolloutItem reports a model-authored additional_tools response
// item and returns its response_item payload plus the harness metadata that
// marks it trusted. Rust persists these as response_item rollout lines rather
// than TurnItems because the protocol TurnItem enum has no additional-tools
// variant (Rust #50435). The model-visible payload matches ResponseItem: an
// optional `id`, a `role`, and arbitrary JSON tool definitions.
func additionalToolsRolloutItem(item *session.Item) (json.RawMessage, json.RawMessage, bool) {
	if item == nil || strings.TrimSpace(item.Type) != "additional_tools" {
		return nil, nil, false
	}
	payload := additionalToolsPayload(item)
	if payload == nil {
		return nil, nil, false
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, false
	}
	return encoded, harnessMetadataFromSessionItem(item), true
}

// additionalToolsPayload prefers an already-serialized response item and falls
// back to reconstructing the wire shape from the session item's fields.
func additionalToolsPayload(item *session.Item) map[string]any {
	if len(item.Raw) > 0 {
		var object map[string]any
		if json.Unmarshal(item.Raw, &object) == nil && object != nil &&
			strings.EqualFold(strings.TrimSpace(stringValue(object, "type")), "additional_tools") {
			return object
		}
	}
	tools, ok := item.Data["tools"]
	if !ok {
		return nil
	}
	payload := map[string]any{
		"type":  "additional_tools",
		"role":  firstNonEmptyString(strings.TrimSpace(item.Role), "developer"),
		"tools": tools,
	}
	if id := strings.TrimSpace(item.ID); id != "" {
		payload["id"] = id
	}
	return payload
}

// harnessMetadataFromSessionItem reads the trusted harness metadata attached to
// a session item, mirroring configurationUpdateRolloutItem's extraction.
func harnessMetadataFromSessionItem(item *session.Item) json.RawMessage {
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
