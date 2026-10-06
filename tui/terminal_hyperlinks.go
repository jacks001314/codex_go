package tui

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"codex_go/utils"
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
		// A URL fragment that runs to the end of the line may have been hard-wrapped
		// by the renderer; annotating a truncated fragment would point the
		// hyperlink at an incomplete target. Skip such fragments (Rust remap_wrapped_line).
		if isLineEnd(line, loc[1]) {
			sb.WriteString(line[cursor:loc[1]])
			cursor = loc[1]
			continue
		}
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
	if end <= start {
		return "", start
	}
	// Mirror Rust's `trailing_url_end` (#51391): count delimiter balances once,
	// then trim trailing sentence punctuation, a trailing `?` used as question
	// punctuation after an unmatched closing delimiter, and unmatched closers.
	var balances [4]int
	for _, ch := range raw[start:end] {
		switch ch {
		case '(':
			balances[0]++
		case ')':
			balances[0]--
		case '[':
			balances[1]++
		case ']':
			balances[1]--
		case '{':
			balances[2]++
		case '}':
			balances[2]--
		case '<':
			balances[3]++
		case '>':
			balances[3]--
		}
	}
	for end > start {
		ch, size := utf8.DecodeLastRuneInString(raw[start:end])
		balanceIndex := -1
		switch ch {
		case ')':
			balanceIndex = 0
		case ']':
			balanceIndex = 1
		case '}':
			balanceIndex = 2
		case '>':
			balanceIndex = 3
		}
		trim := false
		switch {
		case balanceIndex >= 0:
			trim = balances[balanceIndex] < 0
			balances[balanceIndex]++
		case ch == ',' || ch == '.' || ch == ';' || ch == '!' || ch == '\'' || ch == '"':
			trim = true
		case ch == '?':
			// A `?` is question punctuation only when it follows an unmatched
			// closing delimiter; a `?` inside balanced brackets stays in the URL.
			if end-size > start {
				prev, _ := utf8.DecodeLastRuneInString(raw[start : end-size])
				switch prev {
				case ')':
					trim = balances[0] < 0
				case ']':
					trim = balances[1] < 0
				case '}':
					trim = balances[2] < 0
				case '>':
					trim = balances[3] < 0
				}
			}
		}
		if !trim {
			break
		}
		end -= size
	}
	return raw[start:end], start
}
func isLineEnd(line string, pos int) bool {
	if pos >= len(line) {
		return true
	}
	return strings.TrimSpace(utils.StripANSI(line[pos:])) == ""
}
