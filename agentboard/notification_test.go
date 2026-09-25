package agentboard

import (
	"strings"
	"testing"

	"codex_go/agent"
)

// Rust parity: codex-rs/core/src/context/agent_message_board_notification.rs.
func TestAgentMessageBoardNotificationRendersLikeRust(t *testing.T) {
	post := PostPreview{
		PostMetadata: PostMetadata{
			MessageID:   "11111111-1111-4111-8111-111111111111",
			ChannelName: "work",
			Author:      agent.AgentPath("/root/worker"),
			ThreadID:    "11111111-1111-4111-8111-111111111111",
		},
		TextPreview: "hello",
		NChars:      5,
	}
	fragment := NewAgentMessageBoardNotification(post)
	if fragment.Role() != "assistant" {
		t.Fatalf("role = %q", fragment.Role())
	}
	if open, close := fragment.Markers(); open != "" || close != "" {
		t.Fatalf("markers = %q/%q", open, close)
	}
	if fragment.ContentKind() != AgentMessageBoardNotificationContentKind {
		t.Fatalf("content kind = %q", fragment.ContentKind())
	}
	want := "Message Type: CHANNEL_POST\nSender: /root/worker\nChannel: work\n" +
		"Message ID: 11111111-1111-4111-8111-111111111111\n" +
		"Thread ID: 11111111-1111-4111-8111-111111111111\nPayload:\nhello"
	if fragment.Body() != want {
		t.Fatalf("body = %q, want %q", fragment.Body(), want)
	}

	// A truncated preview points the reader at the bounded read tool, and the
	// legacy type markers stay available for matching older rollouts.
	post.Truncated = true
	post.TextPreview = "hello wor"
	truncated := NewAgentMessageBoardNotification(post).Body()
	if !strings.HasSuffix(truncated, "hello wor\n[Use read_post for the rest.]") {
		t.Fatalf("truncated body = %q", truncated)
	}
	if AgentMessageBoardNotificationTypeMarkers[0] != "<agent_message_board_notification>" ||
		AgentMessageBoardNotificationTypeMarkers[1] != "</agent_message_board_notification>" {
		t.Fatalf("type markers = %#v", AgentMessageBoardNotificationTypeMarkers)
	}
}
