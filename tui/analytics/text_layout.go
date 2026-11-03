package analytics

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// analyticsDisplayWidth measures the cells the terminal paints. Analytics rows
// can carry SGR sequences (Rust #46897: the Summary token activity calendar is
// rendered with the terminal palette), so escape sequences are ignored and
// wide characters count as two cells.
func analyticsDisplayWidth(text string) int {
	return ansi.StringWidth(text)
}

// analyticsColumns places left and right on one row with at least one column of
// padding so the row spans width (Rust analytics/render.rs columns, #45764).
func analyticsColumns(left, right string, width int) string {
	padding := width - analyticsDisplayWidth(left) - analyticsDisplayWidth(right)
	if padding < 1 {
		padding = 1
	}
	return left + strings.Repeat(" ", padding) + right
}

// analyticsJoinColumns stacks two card columns without leaving holes below the
// shorter card (Rust analytics/render.rs join_columns).
func analyticsJoinColumns(left, right []string, columnWidth int) []string {
	rows := len(left)
	if len(right) > rows {
		rows = len(right)
	}
	joined := make([]string, 0, rows)
	for row := 0; row < rows; row++ {
		text := ""
		if row < len(left) {
			text = left[row]
		}
		line := analyticsColumns(text, "", columnWidth+4)
		if row < len(right) {
			line += right[row]
		}
		joined = append(joined, line)
	}
	return joined
}

// analyticsTruncateToWidth keeps the longest prefix that fits in width display
// columns, preserving SGR sequences (charmbracelet/x/ansi re-emits the active
// styles after the cut so a truncated styled row does not leak color).
func analyticsTruncateToWidth(text string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(text, width, "")
}

// analyticsTruncate shortens text with an ellipsis instead of cutting a value
// mid-way (Rust truncate_line_with_ellipsis_if_overflow).
func analyticsTruncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if analyticsDisplayWidth(text) <= width {
		return text
	}
	return ansi.Truncate(text, width, "…")
}

// analyticsWrap wraps one row at word boundaries and hard-splits a word that is
// longer than the available width.
func analyticsWrap(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	if analyticsDisplayWidth(text) <= width {
		return []string{text}
	}
	if strings.ContainsRune(text, 0x1b) {
		// Styled rows are already laid out for the target width; clipping an
		// overflow keeps each SGR sequence on the line that opened it instead
		// of stranding styles on the next wrapped row.
		return []string{analyticsTruncate(text, width)}
	}
	indent := text[:len(text)-len(strings.TrimLeft(text, " "))]
	var lines []string
	current := indent
	for _, word := range strings.Fields(text) {
		for analyticsDisplayWidth(word) > width {
			head := analyticsTruncateToWidth(word, width)
			if head == "" {
				break
			}
			if current != "" && current != indent {
				lines = append(lines, current)
				current = indent
			}
			lines = append(lines, head)
			word = strings.TrimPrefix(word, head)
		}
		if word == "" {
			continue
		}
		if current == "" || current == indent {
			if current == "" {
				current = word
			} else {
				current = indent + word
			}
			continue
		}
		if analyticsDisplayWidth(current)+1+analyticsDisplayWidth(word) <= width {
			current += " " + word
			continue
		}
		lines = append(lines, current)
		current = word
	}
	if current != "" && current != indent {
		lines = append(lines, current)
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// analyticsWrapLines wraps each row and keeps the order of the input.
func analyticsWrapLines(lines []string, width int) []string {
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		wrapped = append(wrapped, analyticsWrap(line, width)...)
	}
	return wrapped
}
