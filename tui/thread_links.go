package tui

import (
	"strings"

	"github.com/google/uuid"
)

// Rust parity: codex-rs/tui/src/thread_links.rs (#62332).
//
// Markdown links to a saved conversation on the current app server are handled
// inside the TUI. They must never reach the terminal as OSC 8 links, because a
// terminal hands those to the desktop app instead of switching the conversation
// in place. The escape is kept in the painted frame so the app can hit-test the
// label, and stripped again on the way to the terminal.

// CodexThreadLinkPrefix is the destination prefix of a saved-conversation link.
const CodexThreadLinkPrefix = "codex://threads/"

// ThreadIDFromLink returns the canonical thread id of a `codex://threads/<id>`
// destination. Only an exact, canonical link is navigable: a web URL that
// merely ends in a thread id, a link with an extra path segment, query, or
// fragment, or a non-canonical id all resolve to false.
func ThreadIDFromLink(destination string) (string, bool) {
	id, ok := strings.CutPrefix(destination, CodexThreadLinkPrefix)
	if !ok {
		return "", false
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", false
	}
	canonical := parsed.String()
	if canonical != id {
		return "", false
	}
	return canonical, true
}

// ThreadLinkDestination builds the destination of a saved-conversation link.
func ThreadLinkDestination(threadID string) string {
	return CodexThreadLinkPrefix + strings.TrimSpace(threadID)
}

// MarkdownDestination reports the app-side destination of a Markdown link.
// Saved-conversation links keep their own scheme, while terminal links stay
// restricted to safe web URLs (Rust markdown_destination).
func MarkdownDestination(destination string) (string, bool) {
	if threadID, ok := ThreadIDFromLink(destination); ok {
		return ThreadLinkDestination(threadID), true
	}
	return WebDestination(destination)
}

// StripThreadLinkHyperlinks removes OSC 8 sequences that point at a saved
// conversation while keeping the visible label. Frame output goes through this
// so the terminal never launches the desktop app for a link the TUI handled.
func StripThreadLinkHyperlinks(text string) string {
	if !strings.Contains(text, CodexThreadLinkPrefix) {
		return text
	}
	var out strings.Builder
	index := 0
	for index < len(text) {
		if !strings.HasPrefix(text[index:], "\x1b]8;;") {
			out.WriteByte(text[index])
			index++
			continue
		}
		start := index
		rest := text[index+len("\x1b]8;;"):]
		end, terminator := oscTerminator(rest)
		if end < 0 {
			out.WriteString(text[start:])
			break
		}
		destination := rest[:end]
		index += len("\x1b]8;;") + end + terminator
		if _, ok := ThreadIDFromLink(destination); ok {
			// Drop the opening sequence and the matching closing sequence.
			if closeStart := index; closeStart < len(text) && strings.HasPrefix(text[closeStart:], "\x1b]8;;") {
				closeRest := text[closeStart+len("\x1b]8;;"):]
				closeEnd, closeTerminator := oscTerminator(closeRest)
				if closeEnd >= 0 && closeRest[:closeEnd] == "" {
					index += len("\x1b]8;;") + closeEnd + closeTerminator
				}
			}
			continue
		}
		out.WriteString(text[start:index])
	}
	return out.String()
}
