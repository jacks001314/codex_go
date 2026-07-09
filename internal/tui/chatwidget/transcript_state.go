package chatwidget

import (
	"strings"

	codextui "codex_go/internal/tui"
)

// LastAssistantMarkdown returns the most recent copyable assistant response in
// the visible transcript, matching Rust chatwidget's "copy last response" path.
func LastAssistantMarkdown(messages []codextui.Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role != codextui.RoleAssistant {
			continue
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		return text, true
	}
	return "", false
}
