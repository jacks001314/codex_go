package app

import (
	"testing"

	"codex_go/appserver"
	"codex_go/session"
)

// Rust #60319 side_chat_label: a chat with a title shows it, otherwise the
// first line of its starting prompt is the label.
func TestEmbeddedSideChatChoicePrefersTitleThenPromptLikeRust(t *testing.T) {
	named := embeddedSideChatChoice(session.Record{ID: "saved-1", Title: " Named chat "})
	if named.Name != "Named chat" || named.Preview != "" {
		t.Fatalf("named choice = %#v", named)
	}

	prompted := embeddedSideChatChoice(session.Record{
		ID:    "saved-2",
		Items: []session.Item{{Role: "user", Text: "first prompt\nsecond line"}},
	})
	if prompted.Name != "" || prompted.Preview != "first prompt" {
		t.Fatalf("prompted choice = %#v", prompted)
	}

	fallback := embeddedSideChatChoice(session.Record{ID: "saved-3", Preview: "record preview\nmore"})
	if fallback.Preview != "record preview" {
		t.Fatalf("fallback choice = %#v", fallback)
	}
}

// Rust #60319 latest_side_chat_prompts reads only visible user messages.
func TestSideChatPromptFromThreadItemLikeRust(t *testing.T) {
	user := appserver.ThreadItem{Type: "userMessage", Role: "user", Text: "hello\nworld"}
	if got := sideChatPromptFromThreadItem(user); got != "hello" {
		t.Fatalf("user prompt = %q", got)
	}
	content := appserver.ThreadItem{Type: "userMessage", Content: []appserver.ThreadItemContent{{Text: "from content"}}}
	if got := sideChatPromptFromThreadItem(content); got != "from content" {
		t.Fatalf("content prompt = %q", got)
	}
	assistant := appserver.ThreadItem{Type: "assistantMessage", Role: "assistant", Text: "ignored"}
	if got := sideChatPromptFromThreadItem(assistant); got != "" {
		t.Fatalf("assistant prompt = %q, want empty", got)
	}
	if got := firstSideChatPromptLine("  \n  "); got != "" {
		t.Fatalf("blank prompt = %q, want empty", got)
	}
}
