package agent

import (
	"strings"
	"testing"
)

func TestFormatCompletionMessages(t *testing.T) {
	if _, ok := FormatInterAgentCompletionMessage("/task", "/sender", AgentMessageStatus{Kind: AgentMessageStatusRunning}); ok {
		t.Fatalf("running status should not produce completion")
	}
	message, ok := FormatInterAgentCompletionMessage("/task", "/sender", AgentMessageStatus{Kind: AgentMessageStatusErrored, Message: "boom"})
	if !ok || !strings.Contains(message, "Agent errored: boom") || !strings.Contains(message, ErrorNextAction) {
		t.Fatalf("message = %q/%v", message, ok)
	}
}

func TestFormatSubagentHelpers(t *testing.T) {
	line := FormatSubagentContextLine("/a", "helper")
	if line != "- /a: helper" {
		t.Fatalf("line = %q", line)
	}
	notification := FormatSubagentNotificationMessage("/a", AgentMessageStatus{Kind: AgentMessageStatusCompleted, Message: "done"})
	if !strings.Contains(notification, "status: completed") {
		t.Fatalf("notification = %q", notification)
	}
}
