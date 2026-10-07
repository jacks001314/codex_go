package chatwidget

import (
	codextui "codex_go/tui"
)

// LastAssistantMarkdown returns the most recent copyable assistant response in
// the visible transcript, matching Rust chatwidget's "copy last response" path
// (Rust hands `last_agent_markdown` to the picker unchanged). The response keeps
// its own trailing whitespace so Markdown hard breaks and code padding survive
// the copy (Rust #48549); only trailing blank lines are dropped, and a body that
// is blank after that normalization is not copyable.
func LastAssistantMarkdown(messages []codextui.Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role != codextui.RoleAssistant {
			continue
		}
		text := codextui.NormalizeCompletedAssistantMarkdown(message.Text)
		if text == "" {
			continue
		}
		return text, true
	}
	return "", false
}
