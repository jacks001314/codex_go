package app

import "testing"

// TestInteractiveQuestionEscBackHonorsConfig pins Rust's
// `tui.question_esc_back` default and override (#42889).
func TestInteractiveQuestionEscBackHonorsConfig(t *testing.T) {
	if got := interactiveQuestionEscBack(nil); got == nil || !*got {
		t.Fatalf("default question_esc_back = %#v, want enabled", got)
	}
	if got := interactiveQuestionEscBack(map[string]any{"tui": map[string]any{"question_esc_back": false}}); got == nil || *got {
		t.Fatalf("question_esc_back=false = %#v, want disabled", got)
	}
	if got := interactiveQuestionEscBack(map[string]any{"tui": map[string]any{"question_esc_back": true}}); got == nil || !*got {
		t.Fatalf("question_esc_back=true = %#v, want enabled", got)
	}
}
