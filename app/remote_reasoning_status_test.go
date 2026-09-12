package app

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextea "codex_go/tui/tea"
)

// TestRemoteClientReasoningStatusHeaderLikeRust covers Rust #43921's remote
// path: streaming reasoning summaries publish the latest usable line as the
// working indicator header, and a turn boundary clears it.
func TestRemoteClientReasoningStatusHeaderLikeRust(t *testing.T) {
	messages := make(chan bubbletea.Msg, 16)
	client := &remoteAppServerTUIClient{messages: messages}

	client.recordReasoningSummaryDelta("thread-a", "turn-1", "reasoning-1", "## Step one\n")
	first, ok := (<-messages).(codextea.WorkingStatusHeaderMsg)
	if !ok || first.ThreadID != "thread-a" || first.Text != "Step one" {
		t.Fatalf("first header = %#v", first)
	}

	// The latest usable line wins, and unusable lines do not publish.
	client.recordReasoningSummaryDelta("thread-a", "turn-1", "reasoning-1", "more text")
	second, ok := (<-messages).(codextea.WorkingStatusHeaderMsg)
	if !ok || second.Text != "more text" {
		t.Fatalf("second header = %#v", second)
	}
	// A comment-only line is unusable, so the previous usable line is retained.
	client.recordReasoningSummaryDelta("thread-a", "turn-1", "reasoning-1", "\n<!-- internal note -->")
	third, ok := (<-messages).(codextea.WorkingStatusHeaderMsg)
	if !ok || third.Text != "more text" {
		t.Fatalf("comment line header = %#v, want the previous usable line", third)
	}

	// A turn boundary clears the header and the accumulated buffer.
	client.resetReasoningStatus("thread-a")
	cleared, ok := (<-messages).(codextea.WorkingStatusHeaderMsg)
	if !ok || cleared.ThreadID != "thread-a" || cleared.Text != "" {
		t.Fatalf("cleared header = %#v", cleared)
	}
	if len(client.reasoningBuffers) != 0 {
		t.Fatalf("reasoning buffers = %#v, want cleared", client.reasoningBuffers)
	}
}
