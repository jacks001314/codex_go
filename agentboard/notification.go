package agentboard

// Attributed discussion notices with bounded previews.
//
// Rust parity: codex-rs/core/src/context/agent_message_board_notification.rs.
// The fragment renders as an assistant-role contextual user fragment with no
// markers, carrying a stable content-kind classification.

import (
	"codex_go/context"
)

// AgentMessageBoardNotificationContentKind is the fragment's content-kind
// classification (Rust `content_kind()`).
const AgentMessageBoardNotificationContentKind = "agent_message_board.notification"

// AgentMessageBoardNotificationTypeMarkers are Rust's `type_markers`, kept for
// recognizing notices written before the markerless format. Legacy rollouts
// render these only for matching, never for new content.
var AgentMessageBoardNotificationTypeMarkers = [2]string{
	"<agent_message_board_notification>",
	"</agent_message_board_notification>",
}

// NewAgentMessageBoardNotification renders a discussion notice for a recipient
// turn, mirroring Rust's ContextualUserFragment implementation.
func NewAgentMessageBoardNotification(post PostPreview) context.Fragment {
	suffix := ""
	if post.Truncated {
		suffix = "\n[Use read_post for the rest.]"
	}
	body := "Message Type: CHANNEL_POST\nSender: " + string(post.Author) +
		"\nChannel: " + post.ChannelName +
		"\nMessage ID: " + post.MessageID +
		"\nThread ID: " + post.ThreadID +
		"\nPayload:\n" + post.TextPreview + suffix
	return context.NewSimpleFragmentWithKind("assistant", "", "", body, AgentMessageBoardNotificationContentKind)
}
