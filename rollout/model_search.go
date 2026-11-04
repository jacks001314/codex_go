package rollout

import (
	"encoding/json"
	"strings"
	"time"

	"codex_go/session"
)

// modelSearchCheckpointRolloutItem reports a native-search response item whose
// trusted harness metadata pins future browsing references to the native
// provider. The item also has a normal UI event; this response_item line keeps
// the ownership flag in model history across resume and compaction.
func modelSearchCheckpointRolloutItem(item *session.Item) (json.RawMessage, json.RawMessage, bool) {
	if item == nil {
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
	if used, _ := fields["native_search_fallback_used"].(bool); !used {
		return nil, nil, false
	}
	switch strings.TrimSpace(item.Type) {
	case "web_search_call":
		if len(item.Raw) > 0 {
			var payload map[string]any
			if json.Unmarshal(item.Raw, &payload) == nil && payload != nil {
				encoded, err := json.Marshal(payload)
				if err == nil {
					return encoded, append(json.RawMessage(nil), metadata...), true
				}
			}
		}
	case "webSearch":
	default:
		return nil, nil, false
	}
	payload := map[string]any{
		"type":   "web_search_call",
		"id":     strings.TrimSpace(item.ID),
		"status": "completed",
	}
	if payload["id"] == "" {
		return nil, nil, false
	}
	if action := item.Data["action"]; action != nil {
		payload["action"] = action
	}
	if results, present := item.Data["results"]; present {
		payload["results"] = results
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, false
	}
	return encoded, append(json.RawMessage(nil), metadata...), true
}

func appendModelSearchCheckpoint(recorder *Recorder, item *session.Item, now time.Time) error {
	payload, metadata, ok := modelSearchCheckpointRolloutItem(item)
	if !ok {
		return nil
	}
	return recorder.AppendLine(Line{
		Type:         "response_item",
		Timestamp:    itemTime(item.CreatedAt, now).UTC().Format(time.RFC3339Nano),
		Item:         payload,
		ItemMetadata: metadata,
		ItemID:       item.ID,
		TurnID:       sessionItemExactTurnID(item),
	})
}
