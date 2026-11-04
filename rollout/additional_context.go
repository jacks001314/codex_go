package rollout

import (
	"encoding/json"
	"strings"

	"codex_go/session"
)

// additionalContextToolOutputRolloutItem persists untrusted additional context
// as a ResponseItem with harness provenance. It has no canonical TurnItem
// variant, so it must not be rewritten as a DynamicToolCall.
func additionalContextToolOutputRolloutItem(item *session.Item) (json.RawMessage, json.RawMessage, bool) {
	if item == nil || strings.TrimSpace(item.Type) != "function_call_output" {
		return nil, nil, false
	}
	metadata := harnessMetadataFromSessionItem(item)
	if len(metadata) == 0 {
		return nil, nil, false
	}
	var fields map[string]any
	if json.Unmarshal(metadata, &fields) != nil {
		return nil, nil, false
	}
	if flagged, _ := fields["additional_context_tool_output"].(bool); !flagged {
		return nil, nil, false
	}
	var payload any
	if len(item.Raw) > 0 {
		if json.Unmarshal(item.Raw, &payload) != nil || payload == nil {
			payload = nil
		}
	}
	if payload == nil {
		payload = map[string]any{
			"type":      "function_call_output",
			"name":      firstNonEmptyString(item.Name, stringValue(item.Data, "name")),
			"namespace": firstNonEmptyString(item.Namespace, stringValue(item.Data, "namespace"), "agent"),
			"output":    firstNonEmptyString(item.Text, stringValue(item.Data, "output")),
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, false
	}
	return encoded, append(json.RawMessage(nil), metadata...), true
}
