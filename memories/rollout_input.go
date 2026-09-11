package memories

// Rust parity: codex-rs/memories/write/src/rollout_input.rs (Rust #43800).
// Selects v2 extraction evidence by provenance tier, budgets newest-first within
// each tier, then restores chronological order. Media payloads are replaced with
// placeholders and secrets are redacted.

import (
	"encoding/json"
	"strings"

	"codex_go/rollout"
	"codex_go/safety"
	"codex_go/utils"
)

const (
	rolloutInputOmittedMarker      = "[... response items omitted ...]\n"
	rolloutInputTruncationReserve  = 96
	rolloutInputToolOutputTokens   = 2_000
	rolloutInputMaxRowBytes        = 10_000
	internalMetadataPassthroughKey = "internal_chat_message_metadata_passthrough"
)

type rolloutInputTier int

const (
	rolloutTierHuman rolloutInputTier = iota
	rolloutTierFinal
	rolloutTierOtherAgent
	rolloutTierCommentary
	rolloutTierContext
	rolloutTierTool
)

var rolloutInputTierOrder = []rolloutInputTier{
	rolloutTierHuman,
	rolloutTierFinal,
	rolloutTierOtherAgent,
	rolloutTierCommentary,
	rolloutTierContext,
	rolloutTierTool,
}

func rolloutInputTierLabel(tier rolloutInputTier) string {
	switch tier {
	case rolloutTierHuman:
		return "human user"
	case rolloutTierFinal:
		return "assistant final"
	case rolloutTierCommentary:
		return "assistant commentary"
	case rolloutTierOtherAgent:
		return "other agent"
	case rolloutTierContext:
		return "harness context"
	default:
		return "tool"
	}
}

type rolloutInputRow struct {
	tier rolloutInputTier
	text string
}

// SerializeTieredRolloutForMemory mirrors Rust
// rollout_input::serialize_tiered_input: it renders the v2 extraction evidence
// with provenance tiers and a token budget, selecting newest rows first in each
// tier but emitting them in source order.
func SerializeTieredRolloutForMemory(path string, tokenLimit int) (string, error) {
	lines, _, err := rollout.Load(path)
	if err != nil {
		return "", err
	}
	rows := make([]rolloutInputRow, 0, len(lines))
	userInputCalls := map[string]string{}
	for _, line := range lines {
		item, ok := rolloutInputItem(line, userInputCalls)
		if !ok {
			continue
		}
		rows = append(rows, item)
	}
	if len(rows) == 0 {
		return "", nil
	}

	remaining := utils.ApproxBytesForTokens(tokenLimit) - len(rolloutInputOmittedMarker)
	selected := make([]string, len(rows))
	chosen := make([]bool, len(rows))
	for _, tier := range rolloutInputTierOrder {
		for index := len(rows) - 1; index >= 0; index-- {
			if rows[index].tier != tier || remaining <= len(rolloutInputOmittedMarker)+rolloutInputTruncationReserve {
				continue
			}
			budget := remaining - len(rolloutInputOmittedMarker)
			text := rows[index].text
			if len(text) > budget {
				text = utils.TruncateText(text, utils.BytesPolicy(budget-rolloutInputTruncationReserve))
			}
			remaining -= len(text) + len(rolloutInputOmittedMarker)
			selected[index] = text
			chosen[index] = true
		}
	}

	var rendered strings.Builder
	gap := false
	for index := range rows {
		if chosen[index] {
			rendered.WriteString(selected[index])
			gap = false
			continue
		}
		if !gap {
			rendered.WriteString(rolloutInputOmittedMarker)
			gap = true
		}
	}
	if rendered.Len() > utils.ApproxBytesForTokens(tokenLimit) {
		return "", nil
	}
	return rendered.String(), nil
}

// rolloutInputItem converts one rollout line into a rendered evidence row,
// returning false when the line carries no extractable evidence.
func rolloutInputItem(line rollout.Line, userInputCalls map[string]string) (rolloutInputRow, bool) {
	if line.Type == "inter_agent_communication" {
		var value any
		if json.Unmarshal(line.Payload, &value) != nil {
			return rolloutInputRow{}, false
		}
		return rolloutInputRow{tier: rolloutTierOtherAgent, text: renderRolloutInputRow(rolloutTierOtherAgent, value)}, true
	}
	if line.Type != "item" || len(line.Item) == 0 {
		return rolloutInputRow{}, false
	}
	var raw map[string]any
	if err := json.Unmarshal(line.Item, &raw); err != nil {
		return rolloutInputRow{}, false
	}
	item, ok := sanitizeMemoryResponseItem(raw)
	if !ok {
		return rolloutInputRow{}, false
	}
	replaceRolloutInputMedia(item)

	question, drop := rolloutInputUserQuestion(item, userInputCalls)
	if drop {
		return rolloutInputRow{}, false
	}
	tier := rolloutInputTierForItem(item, question != "")
	text := renderRolloutInputRow(tier, item)
	if question != "" {
		text = safety.RedactSecrets("Assistant question: " + question + "\nHuman reply: " + text)
	}
	return rolloutInputRow{tier: tier, text: text}, true
}

// rolloutInputUserQuestion pairs request_user_input calls with accepted human
// replies, mirroring Rust's pairing logic.
func rolloutInputUserQuestion(item map[string]any, userInputCalls map[string]string) (question string, drop bool) {
	itemType, _ := item["type"].(string)
	callID, _ := item["call_id"].(string)
	switch itemType {
	case "function_call":
		name, _ := item["name"].(string)
		if strings.TrimSpace(name) != "request_user_input" {
			return "", false
		}
		if !rolloutInputDefaultNamespace(item) {
			return "", false
		}
		arguments, _ := item["arguments"].(string)
		userInputCalls[strings.TrimSpace(callID)] = arguments
		return "", true
	case "function_call_output":
		key := strings.TrimSpace(callID)
		arguments, ok := userInputCalls[key]
		if !ok {
			return "", false
		}
		if !rolloutInputReplyHasAnswer(rolloutInputOutputText(item)) {
			return "", false
		}
		delete(userInputCalls, key)
		return arguments, false
	default:
		return "", false
	}
}

func rolloutInputDefaultNamespace(item map[string]any) bool {
	namespace, _ := item["namespace"].(string)
	namespace = strings.TrimSpace(namespace)
	return namespace == "" || namespace == "functions" || namespace == "default"
}

func rolloutInputOutputText(item map[string]any) string {
	for _, key := range []string{"output", "text"} {
		if text, ok := item[key].(string); ok {
			return text
		}
		if blocks, ok := item[key].([]any); ok {
			var builder strings.Builder
			for _, raw := range blocks {
				if block, ok := raw.(map[string]any); ok {
					if text, ok := block["text"].(string); ok {
						builder.WriteString(text)
					}
				}
			}
			if builder.Len() > 0 {
				return builder.String()
			}
		}
	}
	return ""
}

// rolloutInputReplyHasAnswer reports whether a request_user_input reply contains
// at least one non-empty answer.
func rolloutInputReplyHasAnswer(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	var payload struct {
		Answers map[string]struct {
			Answers []string `json:"answers"`
		} `json:"answers"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return false
	}
	for _, answer := range payload.Answers {
		for _, text := range answer.Answers {
			if strings.TrimSpace(text) != "" {
				return true
			}
		}
	}
	return false
}

func rolloutInputTierForItem(item map[string]any, hasQuestion bool) rolloutInputTier {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "function_call_output":
		if hasQuestion {
			return rolloutTierHuman
		}
		return rolloutTierTool
	case "agent_message":
		return rolloutTierOtherAgent
	case "message":
		if rolloutInputMessageIsAgentContent(item) {
			return rolloutTierOtherAgent
		}
		role, _ := item["role"].(string)
		if role == "user" {
			if rolloutInputMessageIsContext(item) {
				return rolloutTierContext
			}
			return rolloutTierHuman
		}
		phase, _ := item["phase"].(string)
		if strings.EqualFold(strings.TrimSpace(phase), "commentary") {
			return rolloutTierCommentary
		}
		return rolloutTierFinal
	default:
		return rolloutTierTool
	}
}

func rolloutInputMessageIsAgentContent(item map[string]any) bool {
	if kinds := rolloutInputContentKinds(item); len(kinds) > 0 {
		for _, kind := range kinds {
			if strings.HasPrefix(kind, "multi_agent.") {
				return true
			}
		}
	}
	for _, text := range rolloutInputMessageTexts(item) {
		trimmed := strings.TrimLeft(text, " \t\r\n")
		if strings.HasPrefix(trimmed, "<subagent_notification>") {
			return true
		}
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 4 && strings.HasPrefix(lines[0], "Message Type:") &&
			strings.HasPrefix(lines[1], "Task name:") &&
			strings.HasPrefix(lines[2], "Sender:") &&
			strings.TrimSpace(lines[3]) == "Payload:" {
			return true
		}
	}
	return false
}

func rolloutInputMessageIsContext(item map[string]any) bool {
	kinds := rolloutInputContentKinds(item)
	if len(kinds) > 0 {
		allNonUser := true
		for _, kind := range kinds {
			if strings.HasPrefix(kind, "user.") {
				allNonUser = false
				break
			}
		}
		if allNonUser {
			return true
		}
	}
	for _, text := range rolloutInputMessageTexts(item) {
		if markedMemoryFragment(strings.TrimSpace(text), "<environment_context>", "</environment_context>") {
			return true
		}
	}
	return false
}

func rolloutInputContentKinds(item map[string]any) []string {
	metadata, ok := item[internalMetadataPassthroughKey].(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := metadata["content_item_kinds"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			out = append(out, text)
		}
	}
	return out
}

func rolloutInputMessageTexts(item map[string]any) []string {
	content, ok := item["content"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(content))
	for _, raw := range content {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := block["text"].(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// replaceRolloutInputMedia replaces image/audio parts with text placeholders.
func replaceRolloutInputMedia(item map[string]any) {
	content, ok := item["content"].([]any)
	if !ok {
		return
	}
	for index, raw := range content {
		block, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		itemType, _ := block["type"].(string)
		switch itemType {
		case "input_image":
			content[index] = map[string]any{"type": "input_text", "text": "[image omitted]"}
		case "input_audio":
			content[index] = map[string]any{"type": "input_text", "text": "[audio omitted]"}
		}
	}
	item["content"] = content
}

func renderRolloutInputRow(tier rolloutInputTier, value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded = []byte("{}")
	}
	text := safety.RedactSecrets(string(encoded))
	if tier == rolloutTierTool && rolloutInputToolOutputLike(value) {
		budget := utils.ApproxBytesForTokens(rolloutInputToolOutputTokens) - rolloutInputTruncationReserve
		text = utils.TruncateText(text, utils.BytesPolicy(budget))
	}
	text = utils.TruncateText(text, utils.BytesPolicy(rolloutInputMaxRowBytes-rolloutInputTruncationReserve))
	return "[" + rolloutInputTierLabel(tier) + "]\n" + text + "\n"
}

func rolloutInputToolOutputLike(value any) bool {
	item, ok := value.(map[string]any)
	if !ok {
		return false
	}
	itemType, _ := item["type"].(string)
	switch itemType {
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
		return true
	default:
		return false
	}
}
