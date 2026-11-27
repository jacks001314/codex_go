package appserver

import (
	"encoding/json"
	"math"
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
	display, displayPresent := entry["display"]
	return moderationContract(moderated, display, displayPresent, promptWellbeingResultsValue(entry))
}

// moderationContract builds the narrowed app-server AgentMessageModeration
// shape. Prompt wellbeing results are optional and malformed values are
// discarded without changing the moderation verdict (Rust #63886).
func moderationContract(moderated bool, display any, displayPresent bool, rawResults any) map[string]any {
	out := map[string]any{"moderated": moderated, "display": nil}
	if results, ok := normalizePromptWellbeingResults(rawResults); ok {
		out["promptWellbeingResults"] = results
	}
	if !displayPresent || display == nil {
		// The app-server contract keeps the nullable display field, so an
		// absent instruction is an explicit null (Rust #60172).
		return out
	}
	normalized, valid := normalizeAgentMessageDisplay(display)
	if !valid {
		// An unreadable instruction leaves the answer restricted.
		out["moderated"] = true
		return out
	}
	out["display"] = normalized
	return out
}

func promptWellbeingResultsValue(entry map[string]any) any {
	if value, ok := entry["promptWellbeingResults"]; ok {
		return value
	}
	return entry["prompt_wellbeing_results"]
}

// normalizePromptWellbeingResults validates every result as one unit. Rust
// deserializes the optional field into Vec<PromptWellbeingResult>; an invalid
// value is dropped while the rest of the moderation decision remains usable.
func normalizePromptWellbeingResults(value any) ([]any, bool) {
	if value == nil {
		return nil, false
	}
	rawResults, ok := value.([]any)
	if !ok {
		return nil, false
	}
	results := make([]any, 0, len(rawResults))
	for _, raw := range rawResults {
		result, ok := normalizePromptWellbeingResult(raw)
		if !ok {
			return nil, false
		}
		results = append(results, result)
	}
	return results, true
}

func normalizePromptWellbeingResult(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	inputItemID, ok := object["inputItemId"].(string)
	if !ok {
		return nil, false
	}
	labels, ok := normalizePromptWellbeingLabels(object["labels"])
	if !ok {
		return nil, false
	}
	rawScores, ok := object["scores"].(map[string]any)
	if !ok {
		return nil, false
	}
	scores := make(map[string]any, len(rawScores))
	for label, score := range rawScores {
		if !isPromptWellbeingScore(score) {
			return nil, false
		}
		scores[label] = score
	}
	return map[string]any{
		"inputItemId": inputItemID,
		"scores":      scores,
		"labels":      labels,
	}, true
}

func normalizePromptWellbeingLabels(value any) ([]any, bool) {
	switch labels := value.(type) {
	case []any:
		out := make([]any, 0, len(labels))
		for _, label := range labels {
			if _, ok := label.(string); !ok {
				return nil, false
			}
			out = append(out, label)
		}
		return out, true
	case []string:
		out := make([]any, 0, len(labels))
		for _, label := range labels {
			out = append(out, label)
		}
		return out, true
	default:
		return nil, false
	}
}

func isPromptWellbeingScore(value any) bool {
	switch number := value.(type) {
	case json.Number:
		_, err := number.Float64()
		return err == nil
	case float64:
		return !math.IsNaN(number) && !math.IsInf(number, 0)
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
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
