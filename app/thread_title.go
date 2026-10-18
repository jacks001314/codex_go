package app

import (
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

const threadTitleMaxChars = 36

type generatedThreadTitle struct {
	Title string `json:"title"`
}

// parseGeneratedThreadTitle normalizes the model's structured title response.
// It removes presentation-only quotes and trailing punctuation while preserving
// meaningful leading punctuation such as ".NET" and "!important".
func parseGeneratedThreadTitle(response string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(response), "{") {
		return "", false
	}

	decoder := json.NewDecoder(strings.NewReader(response))
	decoder.DisallowUnknownFields()
	var generated generatedThreadTitle
	if err := decoder.Decode(&generated); err != nil {
		return "", false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", false
	}

	title := strings.TrimSpace(generated.Title)
	title = strings.Trim(title, "\"'`“”‘’")
	title = strings.Join(strings.Fields(title), " ")
	title = strings.TrimSpace(strings.TrimRight(title, ".?!"))
	if title == "" {
		return "", false
	}

	if utf8.RuneCountInString(title) > threadTitleMaxChars {
		title = string([]rune(title)[:threadTitleMaxChars])
	}
	return title, true
}
