package rollout

import (
	"bytes"
	"encoding/json"

	"codex_go/utils"
)

// Mirrors Rust rollout/src/policy.rs persisted_item_completed_event for
// paginated history: an oversized persisted command output is truncated in the
// middle with an explicit marker so the durable rollout stays bounded.
const (
	persistedCommandOutputMaxBytes         = 64 * 1024
	persistedCommandOutputTruncationMarker = "\n... command output truncated for persistence ...\n"
)

// applyPersistedItemTruncation rewrites the persisted form of a paginated turn
// item, mirroring Rust's processed ItemCompleted representation. It only
// touches CommandExecution aggregated output today (Rust #50427); the MCP
// result cap is tracked separately. Numeric literals are preserved verbatim via
// json.Number so re-encoding does not perturb unrelated fields.
func applyPersistedItemTruncation(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var item map[string]any
	if err := decoder.Decode(&item); err != nil {
		return raw
	}
	if anyString(item, "type") != "CommandExecution" {
		return raw
	}
	output, ok := item["aggregated_output"].(string)
	if !ok || len(output) <= persistedCommandOutputMaxBytes {
		return raw
	}
	item["aggregated_output"] = utils.TruncateMiddleWithMarker(
		output,
		persistedCommandOutputMaxBytes,
		persistedCommandOutputTruncationMarker,
	)
	encoded, err := json.Marshal(item)
	if err != nil {
		return raw
	}
	return encoded
}
