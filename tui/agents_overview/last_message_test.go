package agentsoverview

import (
	"strings"
	"testing"
)

// TestRenderDetailsShowsLastMessage covers Rust #44752: the task details pane
// renders the task's last delivered agent message.
func TestRenderDetailsShowsLastMessage(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Rows[0].LastMessage = "Parser now handles nested groups."
	joined := strings.Join(view.Render(120, 24), "\n")
	for _, want := range []string{"Last message", "Parser now handles nested groups."} {
		if !strings.Contains(joined, want) {
			t.Errorf("details pane missing %q:\n%s", want, joined)
		}
	}
}

// TestRenderDetailsLastMessageUsesMarkdownRenderer covers the injected
// renderer path (the last message is rendered as markdown, like the prompt).
func TestRenderDetailsLastMessageUsesMarkdownRenderer(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Rows[0].LastMessage = "see `code`"
	view.RenderMarkdown = func(text string, width int) []string {
		return []string{"markdown:" + text}
	}
	joined := strings.Join(view.Render(120, 24), "\n")
	if !strings.Contains(joined, "markdown:see `code`") {
		t.Fatalf("details pane did not use the markdown renderer:\n%s", joined)
	}
}

// TestRenderDetailsOmitsMissingLastMessage covers the empty case.
func TestRenderDetailsOmitsMissingLastMessage(t *testing.T) {
	view := New(sampleRows(), "", false)
	joined := strings.Join(view.Render(120, 24), "\n")
	if strings.Contains(joined, "Last message") {
		t.Fatalf("details pane rendered an empty last message:\n%s", joined)
	}
}
