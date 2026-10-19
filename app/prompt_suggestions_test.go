package app

import "testing"

// TestInteractivePromptSuggestionsHonorsConfig pins Rust #47911's
// `tui.prompt_suggestions` config default (disabled) and boolean parsing.
func TestInteractivePromptSuggestionsHonorsConfig(t *testing.T) {
	if got := interactivePromptSuggestions(nil); got == nil || *got {
		t.Fatalf("default prompt_suggestions = %v, want disabled", got)
	}
	if got := interactivePromptSuggestions(map[string]any{"tui": map[string]any{"prompt_suggestions": true}}); got == nil || !*got {
		t.Fatalf("prompt_suggestions=true = %v, want enabled", got)
	}
	if got := interactivePromptSuggestions(map[string]any{"tui": map[string]any{"prompt_suggestions": false}}); got == nil || *got {
		t.Fatalf("prompt_suggestions=false = %v, want disabled", got)
	}
	if got := interactivePromptSuggestions(map[string]any{"tui": map[string]any{"prompt_suggestions": "yes"}}); got == nil || *got {
		t.Fatalf("invalid prompt_suggestions = %v, want disabled default", got)
	}
}
