package rollout

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode"

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
	previewSource := string(serialized)
	// Keep plain output recognizable after truncation; rich results retain
	// their serialized preview so history can distinguish them (#60390).
	if plain, ok := plainMCPTruncationPreview(result); ok {
		previewSource = plain
	}
	previewBudget := maxBytes
	for {
		preview := ""
		if previewBudget > 0 {
			preview = utils.TruncateText(previewSource, utils.BytesPolicy(previewBudget))
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

func plainMCPTruncationPreview(result any) (string, bool) {
	values := mapFromAny(result)
	if values == nil {
		return "", false
	}
	if isError, ok := mcpResultIsError(values); ok && isError {
		return "", false
	}
	for _, key := range []string{"structuredContent", "structured_content", "_meta", "meta"} {
		if values[key] != nil {
			return "", false
		}
	}
	content, _ := values["content"].([]any)
	blocks := make([]string, 0, len(content))
	hasText := false
	for _, raw := range content {
		block := mapFromAny(raw)
		if len(block) != 2 {
			return "", false
		}
		if kind, _ := block["type"].(string); kind != "text" {
			return "", false
		}
		text, ok := block["text"].(string)
		if !ok {
			return "", false
		}
		trimmed := strings.TrimLeftFunc(text, func(r rune) bool {
			return unicode.IsSpace(r) || r == '\ufeff'
		})
		for _, prefix := range []string{"{", "[", "```", "~~~"} {
			if strings.HasPrefix(trimmed, prefix) {
				return "", false
			}
		}
		blocks = append(blocks, text)
		hasText = hasText || text != ""
	}
	if !hasText {
		return "", false
	}
	var preview strings.Builder
	preview.WriteString("Truncated tool output:")
	for _, text := range blocks {
		preview.WriteString("\n")
		preview.WriteString(text)
	}
	return preview.String(), true
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
