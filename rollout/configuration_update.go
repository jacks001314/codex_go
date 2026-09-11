package rollout

import (
	"encoding/json"
	"strings"

	"codex_go/session"
)

// configurationUpdateRolloutItem reports a harness-authored
// configuration_update session item and returns its model-visible response_item
// payload plus the harness metadata that marks it trusted. Rust persists these
// as response_item rollout lines rather than TurnItems because the protocol
// TurnItem enum has no configuration-update variant (#43110).
func configurationUpdateRolloutItem(item *session.Item) (json.RawMessage, json.RawMessage, bool) {
	if item == nil || strings.TrimSpace(item.Type) != "configuration_update" {
		return nil, nil, false
	}
	effort := ""
	if reasoning, ok := item.Data["reasoning"].(map[string]any); ok {
		effort, _ = reasoning["effort"].(string)
	}
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return nil, nil, false
	}
	payload, err := json.Marshal(map[string]any{
		"type":      "configuration_update",
		"reasoning": map[string]any{"effort": effort},
	})
	if err != nil {
		return nil, nil, false
	}
	var metadata json.RawMessage
	switch value := item.Data["harness_metadata"].(type) {
	case json.RawMessage:
		if len(value) > 0 && string(value) != "null" {
			metadata = value
		}
	case string:
		if strings.TrimSpace(value) != "" {
			metadata = json.RawMessage(value)
		}
	}
	return payload, metadata, true
}
