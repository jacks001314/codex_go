package agentsoverview

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// spanStyle is a lightweight ANSI style mirroring the Rust ratatui styles
// used by the agents overview (bold headers, dim meta text, and the status
// dot colors). Styling is applied only by RenderStyled; Render stays plain so
// tests can assert exact text like the Rust snapshot suite (which strips
// styles before comparing).
type spanStyle uint8

const (
	spanPlain spanStyle = iota
	spanBold
	spanDim
	spanRed
	spanGreen
	spanCyan
	spanCyanBold
)

func (s spanStyle) sgr() string {
	switch s {
	case spanBold:
		return "\x1b[1m"
	case spanDim:
		return "\x1b[2m"
	case spanRed:
		return "\x1b[31m"
	case spanGreen:
		return "\x1b[32m"
	case spanCyan:
		return "\x1b[36m"
	case spanCyanBold:
		return "\x1b[36;1m"
	default:
		return ""
	}
}

// span is a styled text segment of a rendered line.
type span struct {
	text  string
	style spanStyle
}

// groupDotStyle maps a status group to its marker color (Rust status()).
func groupDotStyle(group Group) spanStyle {
	switch group {
	case GroupNeedsYou:
		return spanRed
	case GroupWorking:
		return spanGreen
	case GroupReady:
		return spanCyan
	default:
		return spanDim
	}
}

// joinSpans returns the unstyled text of a line.
func joinSpans(spans []span) string {
	var builder strings.Builder
	for _, s := range spans {
		builder.WriteString(s.text)
	}
	return builder.String()
}

// renderStyledSpans encodes a line with ANSI SGR sequences.
func renderStyledSpans(spans []span) string {
	var builder strings.Builder
	for _, s := range spans {
		if s.style == spanPlain || s.text == "" {
			builder.WriteString(s.text)
			continue
		}
		builder.WriteString(s.style.sgr())
		builder.WriteString(s.text)
		builder.WriteString("\x1b[0m")
	}
	return builder.String()
}

// spansWidth is the display width of the unstyled text.
func spansWidth(spans []span) int {
	return runewidth.StringWidth(joinSpans(spans))
}

// truncateSpans cuts a line to maxWidth of display width, keeping styles on
// the surviving segments (the clipped tail may split the last span).
func truncateSpans(spans []span, maxWidth int) []span {
	if maxWidth <= 0 {
		return nil
	}
	remaining := maxWidth
	out := make([]span, 0, len(spans))
	for _, s := range spans {
		width := runewidth.StringWidth(s.text)
		if width == 0 {
			continue
		}
		if width <= remaining {
			out = append(out, s)
			remaining -= width
			continue
		}
		if remaining > 0 {
			out = append(out, span{text: truncateToWidth(s.text, remaining), style: s.style})
		}
		break
	}
	return out
}

// renderLine renders a single line with the given inset prefix.
func renderLine(prefix string, spans []span, maxWidth int, styled bool) string {
	available := maxWidth - runewidth.StringWidth(prefix)
	if available < 0 {
		available = 0
	}
	spans = truncateSpans(spans, available)
	text := joinSpans(spans)
	if styled {
		text = renderStyledSpans(spans)
	}
	return prefix + text
}
