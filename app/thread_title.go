package app

import (
	tuiapp "codex_go/tui/app"
)

const threadTitleMaxChars = tuiapp.ThreadTitleMaxChars

// parseGeneratedThreadTitle normalizes the model's structured title response.
// It removes presentation-only quotes and trailing punctuation while preserving
// meaningful leading punctuation such as ".NET" and "!important".
func parseGeneratedThreadTitle(response string) (string, bool) {
	return tuiapp.ParseGeneratedThreadTitle(response)
}
