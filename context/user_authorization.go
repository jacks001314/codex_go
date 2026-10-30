package context

import (
	"strings"

	"codex_go/eventmap"
)

// IsUserAuthorizationMessage mirrors Rust #41660
// `context::is_user_authorization_message`: it decides from host annotations,
// not from text markers, whether the history should treat an item as genuine
// user input. Contextual fragments (goal updates, environment context, and the
// like) carry non-user classifications and therefore never authorize.
//
// Unknown, incomplete, and legacy metadata stays conservative and counts as
// user authorization, because a missing classification cannot prove the
// message was host-generated.
func IsUserAuthorizationMessage(item *HistoryItem) bool {
	if item == nil || item.Kind != eventmap.ResponseMessage {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(item.Role), "user") {
		return false
	}
	kinds := item.ContentItemKinds
	if len(kinds) == 0 || len(kinds) != len(item.Content) {
		return true
	}
	for _, kind := range kinds {
		if strings.HasPrefix(kind, "user.") {
			return true
		}
		switch kind {
		case "", "unknown",
			// Media preparation can replace real user input.
			"images.preparation_error", "images.unsupported", "audio.unsupported":
			return true
		}
	}
	return false
}
