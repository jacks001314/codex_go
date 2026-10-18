package bottompane

import (
	"testing"
	"time"
)

// TestCustomPromptTextSuggestionPrefillsLikeRust covers Rust #40495: a matching
// suggestion pre-fills the editable rename prompt and swaps the loading label
// for the applied label.
func TestCustomPromptTextSuggestionPrefillsLikeRust(t *testing.T) {
	view := NewCustomPromptView("Name thread", "Type a name", "", "").
		WithTextSuggestion(7, "Generating a title suggestion\u2026", "Suggested from this conversation")
	if view.ContextLabel != "Generating a title suggestion\u2026" {
		t.Fatalf("loading label = %q", view.ContextLabel)
	}
	if !view.ApplyTextSuggestion(7, "Fix login timeout", true) {
		t.Fatal("matching suggestion was not applied")
	}
	if view.Text != "Fix login timeout" || view.Cursor() != len([]rune("Fix login timeout")) {
		t.Fatalf("text/cursor = %q/%d", view.Text, view.Cursor())
	}
	if view.ContextLabel != "Suggested from this conversation" {
		t.Fatalf("applied label = %q", view.ContextLabel)
	}
}

// TestCustomPromptTextSuggestionPreservesManualEditsLikeRust covers the
// user_edited guard: text typed before the response arrives wins.
func TestCustomPromptTextSuggestionPreservesManualEditsLikeRust(t *testing.T) {
	view := NewCustomPromptView("Name thread", "Type a name", "", "").
		WithTextSuggestion(11, "Loading", "Ready")
	view.HandleRuneAt('m', time.Unix(100, 0))
	view.HandleRuneAt('y', time.Unix(101, 0))
	if !view.ApplyTextSuggestion(11, "Generated title", true) {
		t.Fatal("matching suggestion was not consumed")
	}
	if view.Text != "my" {
		t.Fatalf("text = %q, want the manual edit preserved", view.Text)
	}
	if view.ContextLabel != "" {
		t.Fatalf("loading label survived a manual edit: %q", view.ContextLabel)
	}
}

// TestCustomPromptTextSuggestionIgnoresStaleRequestsLikeRust covers request
// correlation.
func TestCustomPromptTextSuggestionIgnoresStaleRequestsLikeRust(t *testing.T) {
	view := NewCustomPromptView("Name thread", "Type a name", "", "").
		WithTextSuggestion(3, "Loading", "Ready")
	if view.ApplyTextSuggestion(4, "stale title", true) {
		t.Fatal("stale request was accepted")
	}
	if view.Text != "" || view.ContextLabel != "Loading" {
		t.Fatalf("stale request changed the prompt: text=%q label=%q", view.Text, view.ContextLabel)
	}
}

// TestCustomPromptTextSuggestionPreservesPastedTextLikeRust covers explicit
// pastes marking the prompt as user-edited.
func TestCustomPromptTextSuggestionPreservesPastedTextLikeRust(t *testing.T) {
	view := NewCustomPromptView("Name thread", "Type a name", "", "").
		WithTextSuggestion(5, "Loading", "Ready")
	if !view.HandlePaste("pasted name") {
		t.Fatal("paste was rejected")
	}
	view.ApplyTextSuggestion(5, "generated title", true)
	if view.Text != "pasted name" {
		t.Fatalf("text = %q, want the pasted text", view.Text)
	}
}
