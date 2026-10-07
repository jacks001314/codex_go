package tui

import "strings"

// NormalizeCompletedAssistantMarkdown reproduces the whitespace half of the
// visible-markdown normalization Rust applies to a completed assistant response
// in `git_action_directives::parse_assistant_markdown` (#48549): the response is
// split with `str::lines` semantics -- on '\n', dropping the '\r' of a "\r\n"
// terminator and not yielding an empty final element for a trailing newline --
// and trailing whitespace-only lines are dropped. Every other byte is preserved
// verbatim, so the trailing whitespace that Markdown hard breaks and code
// padding depend on survives copying and `/copy`.
//
// Rust strips assistant directives in the same pass; Go has no `::git-*`
// directive stripper (see tui/git_action_directives.go), so only the whitespace
// half applies here.
func NormalizeCompletedAssistantMarkdown(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	// `str::lines()` treats the final line ending as optional: a trailing '\n'
	// terminates the last line instead of starting a new empty one.
	trailingNewline := lines[len(lines)-1] == ""
	if trailingNewline {
		lines = lines[:len(lines)-1]
	}
	for i := range lines {
		// Only a '\r' that belongs to a "\r\n" terminator is dropped; a bare
		// carriage return inside the text is content, as it is in Rust.
		if i < len(lines)-1 || trailingNewline {
			lines[i] = strings.TrimSuffix(lines[i], "\r")
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
