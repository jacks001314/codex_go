package tui

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Rust parity: codex-rs/tui/src/terminal_hyperlinks.rs.

type TerminalHyperlink struct {
	Start       int
	End         int
	Destination string
}

// AnnotateWebURLsInLine wraps web URLs in a rendered terminal line with OSC-8
// hyperlink sequences. ANSI styling around a URL (e.g. cyan link text) is
// preserved because the URL bytes are a contiguous run that is wrapped in place.
func AnnotateWebURLsInLine(line string) string {
	if !strings.Contains(line, "http") {
		return line
	}
	locations := webURLPattern.FindAllStringIndex(line, -1)
	if len(locations) == 0 {
		return line
	}
	var sb strings.Builder
	cursor := 0
	for _, loc := range locations {
		raw := line[loc[0]:loc[1]]
		trimmed, trimStart := trimWebToken(raw)
		if _, ok := WebDestination(trimmed); !ok {
			continue
		}
		urlStart := loc[0] + trimStart
		sb.WriteString(line[cursor:urlStart])
		sb.WriteString(OSC8Hyperlink(trimmed, trimmed))
		cursor = urlStart + len(trimmed)
	}
	sb.WriteString(line[cursor:])
	return sb.String()
}

var webURLPattern = regexp.MustCompile(`https?://[^\s\x1b]+`)

func WebDestination(destination string) (string, bool) {
	safe := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, destination)
	parsed, err := url.Parse(safe)
	if err != nil || parsed.Host == "" {
		return "", false
	}
	switch parsed.Scheme {
	case "http", "https":
		return safe, true
	default:
		return "", false
	}
}

func OSC8Hyperlink(destination string, text string) string {
	safe, ok := WebDestination(destination)
	if !ok {
		return text
	}
	return "\x1b]8;;" + safe + "\x07" + text + "\x1b]8;;\x07"
}

func StripOSC8(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		if strings.HasPrefix(text[i:], "\x1b]8;;") {
			i += len("\x1b]8;;")
			for i < len(text) && text[i] != '\a' {
				i++
			}
			if i < len(text) {
				i++
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		out.WriteRune(r)
		i += size
	}
	return out.String()
}

func WebLinksInText(text string) []TerminalHyperlink {
	links := []TerminalHyperlink{}
	searchFrom := 0
	for _, raw := range strings.Fields(text) {
		index := strings.Index(text[searchFrom:], raw)
		if index < 0 {
			continue
		}
		rawStart := searchFrom + index
		searchFrom = rawStart + len(raw)
		candidate, offset := trimWebToken(raw)
		if destination, ok := WebDestination(candidate); ok {
			start := DisplayWidth(text[:rawStart+offset])
			links = append(links, TerminalHyperlink{
				Start:       start,
				End:         start + DisplayWidth(candidate),
				Destination: destination,
			})
		}
	}
	return links
}

func trimWebToken(raw string) (string, int) {
	start := 0
	for start < len(raw) && strings.ContainsRune("()[]{}<>,.;!'\"", rune(raw[start])) {
		start++
	}
	end := len(raw)
	for end > start && strings.ContainsRune(",.;!'\"", rune(raw[end-1])) {
		end--
	}
	for end > start && strings.ContainsRune(")]}>", rune(raw[end-1])) && unmatchedClosing(raw[start:end], rune(raw[end-1])) {
		end--
	}
	return raw[start:end], start
}

func unmatchedClosing(text string, closing rune) bool {
	opening := map[rune]rune{')': '(', ']': '[', '}': '{', '>': '<'}[closing]
	if opening == 0 {
		return false
	}
	openCount := strings.Count(text, string(opening))
	closeCount := strings.Count(text, string(closing))
	return closeCount > openCount
}
