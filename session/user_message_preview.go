package session

import (
	"strings"

	"codex_go/eventmap"
)

// userMessageBeginMarker is Rust protocol::USER_MESSAGE_BEGIN: the boundary
// between hidden prior context and the text the user typed.
const userMessageBeginMarker = "<user_message>"

// IsContextualUserMessage reports whether a user-role item carries hidden
// runtime context instead of user input (Rust
// core/src/event_mapping.rs::is_contextual_user_message_content). The
// "# AGENTS.md instructions" block and every other contextual fragment are
// excluded from previews and history rollups.
func IsContextualUserMessage(item *Item) bool {
	if item == nil || item.Role != "user" {
		return false
	}
	if len(item.Content) == 0 {
		if strings.TrimSpace(item.Text) == "" {
			return false
		}
		return eventmap.IsContextualUserMessageContent([]eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: item.Text}})
	}
	parts := make([]eventmap.ContentItem, 0, len(item.Content))
	for _, part := range item.Content {
		parts = append(parts, eventmap.ContentItem{Kind: eventmap.ContentKind(part.Type), Text: part.Text})
	}
	return eventmap.IsContextualUserMessageContent(parts)
}

// UserMessagePreview renders the thread-list preview for a user item, mirroring
// Rust protocol::user_message_preview (#63603): hidden context before the user
// message marker is stripped and media-only input degrades to [Image]/[Audio].
// Contextual fragments report false so callers keep looking for real input.
func UserMessagePreview(item *Item) (string, bool) {
	if item == nil || item.Role != "user" || IsContextualUserMessage(item) {
		return "", false
	}
	text := item.Text
	if strings.TrimSpace(text) == "" {
		parts := make([]string, 0, len(item.Content))
		for _, part := range item.Content {
			if strings.TrimSpace(part.Text) != "" {
				parts = append(parts, part.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}
	if preview := StripUserMessagePrefix(text); preview != "" {
		return preview, true
	}
	hasImage, hasAudio := false, false
	for _, part := range item.Content {
		if strings.Contains(part.Type, "audio") {
			hasAudio = true
			continue
		}
		if part.ImageURL != "" || part.FileID != "" {
			hasImage = true
		}
	}
	if hasImage {
		return "[Image]", true
	}
	if hasAudio {
		return "[Audio]", true
	}
	return "", false
}

// StripUserMessagePrefix mirrors Rust protocol::strip_user_message_prefix: keep
// everything after the first user message marker.
func StripUserMessagePrefix(text string) string {
	if idx := strings.Index(text, userMessageBeginMarker); idx >= 0 {
		return strings.TrimSpace(text[idx+len(userMessageBeginMarker):])
	}
	return strings.TrimSpace(text)
}
