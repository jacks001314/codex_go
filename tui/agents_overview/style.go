package agentsoverview

import (
	"strconv"
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
	// color is an optional "#rrggbb" foreground applied after style, used for
	// deterministic per-thread identity colors (Rust #44857 thread_color).
	color string
	// raw marks text that is already styled (for example markdown rendered to
	// ANSI). Raw text is emitted as-is when styled and stripped for plain
	// rendering, and its width is measured with the escape sequences removed
	// (Rust #44752).
	raw bool
}

// stripANSISGR removes SGR escape sequences so plain rendering and width
// measurement ignore pre-styled content.
func stripANSISGR(value string) string {
	if !strings.Contains(value, "\x1b[") {
		return value
	}
	var builder strings.Builder
	for i := 0; i < len(value); {
		if value[i] == 0x1b && i+1 < len(value) && value[i+1] == '[' {
			j := i + 2
			for j < len(value) && (value[j] < 0x40 || value[j] > 0x7e) {
				j++
			}
			if j < len(value) {
				j++
			}
			i = j
			continue
		}
		builder.WriteByte(value[i])
		i++
	}
	return builder.String()
}

func ansiAwareWidth(value string) int {
	return runewidth.StringWidth(stripANSISGR(value))
}

// threadTitleSpan renders a row title with the thread's identity color when one
// is available, otherwise with the given fallback style.
func threadTitleSpan(title string, color string, fallback spanStyle) span {
	if color != "" {
		return span{text: title, style: spanPlain, color: color}
	}
	return span{text: title, style: fallback}
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

// threadColorSGR converts a "#rrggbb" identity color to a truecolor foreground
// SGR sequence, matching the tui package's thread color encoding.
func threadColorSGR(color string) string {
	color = strings.TrimPrefix(strings.TrimSpace(color), "#")
	if len(color) != 6 {
		return ""
	}
	value, err := strconv.ParseUint(color, 16, 32)
	if err != nil {
		return ""
	}
	return "\x1b[38;2;" + strconv.Itoa(int(value>>16)&0xff) + ";" + strconv.Itoa(int(value>>8)&0xff) + ";" + strconv.Itoa(int(value)&0xff) + "m"
}

// renderStyledSpans encodes a line with ANSI SGR sequences.
func renderStyledSpans(spans []span) string {
	var builder strings.Builder
	for _, s := range spans {
		if s.text == "" {
			builder.WriteString(s.text)
			continue
		}
		if s.raw {
			builder.WriteString(s.text)
			continue
		}
		switch {
		case s.color != "":
			if sgr := threadColorSGR(s.color); sgr != "" {
				builder.WriteString(sgr)
				builder.WriteString(s.text)
				builder.WriteString("\x1b[39m")
				continue
			}
			builder.WriteString(s.text)
		case s.style == spanPlain:
			builder.WriteString(s.text)
		default:
			builder.WriteString(s.style.sgr())
			builder.WriteString(s.text)
			builder.WriteString("\x1b[0m")
		}
	}
	return builder.String()
}

// spansWidth is the display width of the unstyled text.
func spansWidth(spans []span) int {
	return ansiAwareWidth(joinSpans(spans))
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
		width := ansiAwareWidth(s.text)
		if width == 0 {
			continue
		}
		if width <= remaining {
			out = append(out, s)
			remaining -= width
			continue
		}
		if s.raw {
			// Pre-rendered lines are already wrapped by their renderer; drop a
			// line that still does not fit rather than splitting escape codes.
			break
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
	text := ""
	for _, s := range spans {
		if s.raw {
			text += stripANSISGR(s.text)
			continue
		}
		text += s.text
	}
	if styled {
		text = renderStyledSpans(spans)
	}
	return prefix + text
}
