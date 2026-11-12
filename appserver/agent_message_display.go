package appserver

import (
	"encoding/json"
	"strings"
)

// normalizeFinalAnswerModeration mirrors Rust #60904
// (protocol/src/items/display.rs): the stream's final_answer_moderation entry
// is narrowed to the versioned AgentMessageModeration contract, and a display
// instruction it cannot read leaves the answer restricted.
func normalizeFinalAnswerModeration(metadata any) any {
	entry, ok := moderationEntry(metadata)
	if !ok {
		return metadata
	}
	moderated := false
	if value, ok := entry["moderated"].(bool); ok {
		moderated = value
	}
	display, ok := entry["display"]
	if !ok || display == nil {
		return map[string]any{"moderated": moderated}
	}
	normalized, valid := normalizeAgentMessageDisplay(display)
	if !valid {
		// An unreadable instruction leaves the answer restricted.
		return map[string]any{"moderated": true}
	}
	return map[string]any{"moderated": moderated, "display": normalized}
}

// moderationEntry accepts the stream metadata wrapper and the bare entry that
// older rollouts stored on the item.
func moderationEntry(metadata any) (map[string]any, bool) {
	object, ok := metadata.(map[string]any)
	if !ok {
		if typed, ok := metadata.(json.RawMessage); ok {
			if err := json.Unmarshal(typed, &object); err != nil {
				return nil, false
			}
		} else {
			return nil, false
		}
	}
	if nested, ok := object["final_answer_moderation"].(map[string]any); ok {
		return nested, true
	}
	if nested, ok := object["finalAnswerModeration"].(map[string]any); ok {
		return nested, true
	}
	if _, ok := object["moderated"]; ok {
		return object, true
	}
	return nil, false
}

// normalizeAgentMessageDisplay validates one display instruction. Only version
// 1 is supported; every required field of the selected mode must be present.
func normalizeAgentMessageDisplay(display any) (map[string]any, bool) {
	object, ok := display.(map[string]any)
	if !ok {
		return nil, false
	}
	if version, ok := jsonIntValue(object["version"]); !ok || version != 1 {
		return nil, false
	}
	mode, _ := object["mode"].(string)
	switch strings.TrimSpace(mode) {
	case "show":
		annotation, ok := object["annotation"].(map[string]any)
		if !ok {
			return nil, false
		}
		kind, _ := annotation["kind"].(string)
		subject, _ := annotation["subject"].(string)
		if kind != "wellbeingHelpline" || (subject != "answer" && subject != "prompt") {
			return nil, false
		}
		contacts, _ := object["trustedContacts"].(string)
		if contacts != "check" && contacts != "skip" {
			return nil, false
		}
		return map[string]any{
			"version":         1,
			"mode":            "show",
			"trustedContacts": contacts,
			"annotation":      map[string]any{"kind": kind, "subject": subject},
		}, true
	case "hide":
		replacement, ok := object["replacement"].(map[string]any)
		if !ok {
			return nil, false
		}
		kind, _ := replacement["kind"].(string)
		if kind != "wellbeingPromptHelpline" {
			return nil, false
		}
		return map[string]any{
			"version":     1,
			"mode":        "hide",
			"replacement": map[string]any{"kind": kind},
		}, true
	case "block":
		reason, ok := object["reason"].(map[string]any)
		if !ok {
			return nil, false
		}
		kind, _ := reason["kind"].(string)
		switch kind {
		case "protection":
			protectionType, _ := reason["protectionType"].(string)
			if strings.TrimSpace(protectionType) == "" {
				return nil, false
			}
			return map[string]any{
				"version": 1,
				"mode":    "block",
				"reason":  map[string]any{"kind": kind, "protectionType": protectionType},
			}, true
		case "cyber":
			normalized := map[string]any{"kind": kind}
			if trusted, ok := reason["trustedAccess"].(bool); ok {
				normalized["trustedAccess"] = trusted
			}
			return map[string]any{
				"version": 1,
				"mode":    "block",
				"reason":  normalized,
			}, true
		}
	}
	return nil, false
}

func jsonIntValue(value any) (int64, bool) {
	switch number := value.(type) {
	case float64:
		if number != float64(int64(number)) {
			return 0, false
		}
		return int64(number), true
	case int:
		return int64(number), true
	case int64:
		return number, true
	case json.Number:
		parsed, err := number.Int64()
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}
