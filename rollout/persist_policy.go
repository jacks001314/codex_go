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
	persistedMCPResultMaxBytes             = 64 * 1024
)

// applyPersistedItemTruncation rewrites the persisted form of a paginated turn
// item, mirroring Rust's processed ItemCompleted representation
// (rollout/src/policy.rs). CommandExecution aggregated output is capped at 64
// KiB (#50427) and oversized MCP tool results are replaced with a bounded text
// preview (#50458/#50470). Numeric literals are preserved verbatim via
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
	changed := false
	switch anyString(item, "type") {
	case "CommandExecution":
		if output, ok := item["aggregated_output"].(string); ok && len(output) > persistedCommandOutputMaxBytes {
			item["aggregated_output"] = utils.TruncateMiddleWithMarker(
				output,
				persistedCommandOutputMaxBytes,
				persistedCommandOutputTruncationMarker,
			)
			changed = true
		}
	case "McpToolCall":
		if result, ok := item["result"]; ok && result != nil {
			if truncated, ok := truncateMCPResultValue(result, persistedMCPResultMaxBytes); ok {
				item["result"] = truncated
				changed = true
			}
		}
	}
	if !changed {
		return raw
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return raw
	}
	return encoded
}

// truncateMCPResultValue mirrors Rust `truncate_mcp_tool_result`
// (codex-rs/utils/output-truncation/src/lib.rs, #50458/#50470): an oversized
// serialized MCP result is replaced with a single text preview bounded by
// maxBytes, preserving the error flag and dropping structured content/meta. The
// preview budget is progressively reduced to absorb JSON escaping overhead, and
// the function is idempotent for an already-bounded result.
func truncateMCPResultValue(result any, maxBytes int) (any, bool) {
	serialized, err := json.Marshal(result)
	if err != nil || len(serialized) <= maxBytes {
		return result, false
	}
	isError, hasIsError := mcpResultIsError(mapFromAny(result))
	previewBudget := maxBytes
	for {
		preview := ""
		if previewBudget > 0 {
			preview = utils.TruncateText(string(serialized), utils.BytesPolicy(previewBudget))
		}
		truncated := map[string]any{
			"content": []any{map[string]any{"type": "text", "text": preview}},
		}
		if hasIsError {
			truncated["isError"] = isError
		}
		encoded, err := json.Marshal(truncated)
		if err != nil {
			return result, false
		}
		if len(encoded) <= maxBytes || previewBudget == 0 {
			return truncated, true
		}
		scaled := previewBudget * maxBytes / len(encoded)
		if limit := previewBudget - 1; scaled > limit {
			scaled = limit
		}
		previewBudget = scaled
	}
}

func mcpResultIsError(result map[string]any) (bool, bool) {
	if result == nil {
		return false, false
	}
	for _, key := range []string{"isError", "is_error"} {
		if value, ok := result[key]; ok {
			if flag, ok := value.(bool); ok {
				return flag, true
			}
		}
	}
	return false, false
}
