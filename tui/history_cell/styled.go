package historycell

// Styled history lines. The base HistoryCell contract stays plain text
// (DisplayLines/RawLines) so every existing cell and caller keeps working; a
// cell that can carry colour additionally implements StyledHistoryCell, and the
// renderer uses the styled projection when it is available.

import "strings"

// CellStyle describes one span's presentation. Empty colour strings inherit the
// surrounding style. Colours are lipgloss-compatible colour strings, matching
// the ANSI indices the Rust TUI uses through ratatui (for example "7" for Gray,
// "8" for DarkGray, "14" for Cyan, "13" for Magenta, "0" for Black).
type CellStyle struct {
	Foreground string
	Background string
	Bold       bool
	Dim        bool
	Italic     bool
	Underline  bool
}

// StyledSpan is one run of text with one style.
type StyledSpan struct {
	Text  string
	Style CellStyle
}

// StyledLine is a sequence of styled spans.
type StyledLine []StyledSpan

// StyledHistoryCell is implemented by cells that can render colour.
type StyledHistoryCell interface {
	DisplayStyledLines(width int) []StyledLine
	RawLines() []string
}

// Plain flattens the line to text.
func (l StyledLine) Plain() string {
	var builder strings.Builder
	for _, span := range l {
		builder.WriteString(span.Text)
	}
	return builder.String()
}

// PlainLines flattens styled lines to text.
func PlainLines(lines []StyledLine) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, line.Plain())
	}
	return out
}

// StyledLinesFromPlain wraps plain lines as unstyled spans.
func StyledLinesFromPlain(lines []string) []StyledLine {
	out := make([]StyledLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, StyledLine{{Text: line}})
	}
	return out
}
