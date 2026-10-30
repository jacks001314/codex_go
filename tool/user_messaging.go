package tool

import (
	"strings"

	codexctx "codex_go/context"
)

// guardianDeliveredAssistantMessageTokens mirrors Rust
// `GUARDIAN_MAX_ROOT_MESSAGE_TOKENS`: one retained root message's budget.
const guardianDeliveredAssistantMessageTokens = 900

// captureDeliveredAssistantMessage records the text a successful host-owned
// User Messaging call confirmed. The post-tool payload carries the final,
// hook-rewritten arguments, so attempted or unconfirmed arguments never become
// delivery evidence (Rust #47030 `AnyToolResult::delivered_assistant_message`).
func captureDeliveredAssistantMessage(output *Output, payload *PostToolUsePayload) {
	if output == nil || !output.Success || payload == nil || payload.ToolName == nil {
		return
	}
	switch strings.TrimSpace(payload.ToolName.Name) {
	case "mcp__codex_apps__user_messaging__send_message",
		"mcp__codex_apps__user_messaging_send_message",
		"mcp__codex_apps__user_message__send_message",
		"mcp__codex_apps__user_message_send_message":
	default:
		return
	}
	input, ok := payload.ToolInput.(map[string]any)
	if !ok {
		return
	}
	text, ok := input["text"].(string)
	if !ok || strings.TrimSpace(text) == "" {
		return
	}
	bounded, _ := codexctx.GuardianTruncateText(text, guardianDeliveredAssistantMessageTokens)
	output.DeliveredAssistantMessage = bounded
}
