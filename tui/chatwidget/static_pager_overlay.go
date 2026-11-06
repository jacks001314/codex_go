package chatwidget

import (
	"fmt"
	"strings"
)

// staticPagerHeader mirrors Rust PagerView::render_header: a dim "/ " fill
// across the row with "/ TITLE" drawn over its start.
func staticPagerHeader(width int, title string) string {
	if width <= 0 {
		return "/ " + strings.TrimSpace(title)
	}
	fill := []rune(strings.Repeat("/ ", (width+1)/2))
	if len(fill) > width {
		fill = fill[:width]
	}
	prefix := []rune("/ " + strings.TrimSpace(title))
	for index := range fill {
		if index >= len(prefix) {
			break
		}
		fill[index] = prefix[index]
	}
	if len(prefix) > len(fill) {
		return string(prefix[:len(fill)])
	}
	return string(fill)
}

// staticPagerSeparator mirrors Rust PagerView::render_bottom_bar: a dim rule
// across the row with " N% " ending one column before the edge.
func staticPagerSeparator(width int, percent int) string {
	if width <= 0 {
		return ""
	}
	bar := []rune(strings.Repeat("\u2500", width))
	pct := []rune(fmt.Sprintf(" %d%% ", percent))
	start := width - len(pct) - 1
	if start < 0 {
		start = 0
	}
	for index := range pct {
		if start+index >= width {
			break
		}
		bar[start+index] = pct[index]
	}
	return string(bar)
}

// staticPagerBody draws the scrolled content and fills the remaining rows with
// Rust's "~" gutter.
func (o *TranscriptOverlay) staticPagerBody(width int, height int) []string {
	lines := strings.Split(o.content, "\n")
	offset := o.viewport.YOffset()
	rows := make([]string, 0, height)
	for index := 0; index < height; index++ {
		contentIndex := offset + index
		if contentIndex < len(lines) {
			rows = append(rows, truncateOverlayLine(lines[contentIndex], width))
			continue
		}
		rows = append(rows, "~"+strings.Repeat(" ", max(0, width-1)))
	}
	return rows
}

func truncateOverlayLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = strings.ReplaceAll(line, "\r", "")
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	return string(runes[:width])
}
