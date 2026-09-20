package bottompane

import (
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/selection_tabs.rs.
//
// Single-row filled tab headers for selection lists. Filled tabs always retain
// the active tab and reserve arrows for hidden neighbours.

// FilledTabMarkerLeft / FilledTabMarkerRight are Rust's overflow markers.
const (
	FilledTabMarkerLeft  = "\u2039"
	FilledTabMarkerRight = "\u203a"
	// FilledTabMarkerColumns is the columns a marker reserves (the glyph plus a
	// separating space).
	FilledTabMarkerColumns = 2
)

// FilledTabCell is one rendered tab cell (or overflow marker) of a filled tab
// bar, together with the columns it occupies.
type FilledTabCell struct {
	// Index is the tab index, or -1 for an overflow marker.
	Index int
	// Start and Width are the cell's columns within the bar.
	Start int
	Width int
	// Text is the cell's content, already truncated to Width with an ellipsis.
	Text string
	// Active reports whether this cell is the selected tab.
	Active bool
}

// FilledTabBar mirrors Rust's `render_filled_tab_bar`: the window of tabs around
// the active one that fits the width, with a marker on each side that hides
// neighbours. Cells are returned in render order; the caller paints the active
// cell with the active-tab style and the others dim.
func FilledTabBar(labels []string, activeIdx int, width int) []FilledTabCell {
	if len(labels) == 0 || width <= 0 {
		return nil
	}
	if activeIdx < 0 {
		activeIdx = 0
	}
	if activeIdx >= len(labels) {
		activeIdx = len(labels) - 1
	}
	widths := make([]int, len(labels))
	for index, label := range labels {
		widths[index] = tui.DisplayWidth(label) + 2
	}
	occupied := func(start int, end int) int {
		total := end - start - 1
		for _, cellWidth := range widths[start:end] {
			total += cellWidth
		}
		if start > 0 {
			total += FilledTabMarkerColumns
		}
		if end < len(labels) {
			total += FilledTabMarkerColumns
		}
		return total
	}
	start, end := activeIdx, activeIdx+1
	for start > 0 && occupied(start-1, end) <= width {
		start--
	}
	for end < len(labels) && occupied(start, end+1) <= width {
		end++
	}
	// Tiny strips prioritize a readable active label over navigation hints.
	showLeft := start > 0 && width >= 5
	showRight := end < len(labels) && width >= 7

	right := width
	if showRight {
		right -= FilledTabMarkerColumns
	}
	cells := make([]FilledTabCell, 0, end-start+2)
	x := 0
	if showLeft {
		cells = append(cells, FilledTabCell{Index: -1, Start: x, Width: 1, Text: FilledTabMarkerLeft})
		x += FilledTabMarkerColumns
	}
	for index := start; index < end; index++ {
		cellWidth := min(widths[index], max(right-x, 0))
		text := tui.TruncateWithEllipsis(" "+labels[index]+" ", cellWidth)
		cells = append(cells, FilledTabCell{
			Index:  index,
			Start:  x,
			Width:  cellWidth,
			Text:   text,
			Active: index == activeIdx,
		})
		x += cellWidth + 1
	}
	if showRight {
		cells = append(cells, FilledTabCell{
			Index: -1,
			Start: width - 1,
			Width: 1,
			Text:  FilledTabMarkerRight,
		})
	}
	return cells
}

// FilledTabBarText renders the bar's plain text, the way Rust's buffer shows it:
// each cell at its column with the gaps left blank.
func FilledTabBarText(labels []string, activeIdx int, width int) string {
	cells := FilledTabBar(labels, activeIdx, width)
	if len(cells) == 0 || width <= 0 {
		return ""
	}
	var builder strings.Builder
	column := 0
	for _, cell := range cells {
		if cell.Start > column {
			builder.WriteString(strings.Repeat(" ", cell.Start-column))
			column = cell.Start
		}
		builder.WriteString(cell.Text)
		column += tui.DisplayWidth(cell.Text)
	}
	if trailing := width - column; trailing > 0 {
		builder.WriteString(strings.Repeat(" ", trailing))
	}
	return builder.String()
}

// FilledTabBarLines reports the bar's lines: a filled tab bar is always one row
// (Rust's tab_bar_height).
func FilledTabBarLines(labels []string, activeIdx int, width int) []string {
	if len(labels) == 0 || width <= 0 {
		return nil
	}
	return []string{strings.TrimRight(FilledTabBarText(labels, activeIdx, width), " ")}
}

// FilledTabBarStyledText renders the bar with the active cell styled by
// styleActive and every other cell (including the overflow markers) by
// styleInactive, the way Rust paints the active tab with its fill and the
// remaining cells dim.
func FilledTabBarStyledText(
	labels []string,
	activeIdx int,
	width int,
	styleActive func(string) string,
	styleInactive func(string) string,
) string {
	cells := FilledTabBar(labels, activeIdx, width)
	if len(cells) == 0 || width <= 0 {
		return ""
	}
	var builder strings.Builder
	column := 0
	for _, cell := range cells {
		if cell.Start > column {
			builder.WriteString(strings.Repeat(" ", cell.Start-column))
			column = cell.Start
		}
		style := styleInactive
		if cell.Active {
			style = styleActive
		}
		text := cell.Text
		if style != nil {
			text = style(text)
		}
		builder.WriteString(text)
		column += tui.DisplayWidth(cell.Text)
	}
	if trailing := width - column; trailing > 0 {
		builder.WriteString(strings.Repeat(" ", trailing))
	}
	return builder.String()
}
