package tui

import "testing"

// Rust #48549: the completed response keeps every line's own trailing
// whitespace; only a trailing newline and trailing blank lines are normalized
// away, following `str::lines` semantics.
func TestNormalizeCompletedAssistantMarkdownMatchesRustLines(t *testing.T) {
	for input, want := range map[string]string{
		"":                         "",
		"a  ":                      "a  ",
		"a  \n":                    "a  ",
		"a  \n\n":                  "a  ",
		"a\r\nb\t\r\n":             "a\nb\t",
		"a\rb":                     "a\rb",
		"a\r":                      "a\r",
		"```go\ncode  \n```\n\n\n": "```go\ncode  \n```",
		"   \n":                    "",
		"\n":                       "",
	} {
		if got := NormalizeCompletedAssistantMarkdown(input); got != want {
			t.Fatalf("NormalizeCompletedAssistantMarkdown(%q) = %q, want %q", input, got, want)
		}
	}
}
