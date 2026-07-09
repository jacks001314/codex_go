package bottompane

import "codex_go/internal/tui"

// Rust parity: codex-rs/tui/src/bottom_pane/selection_tabs.rs.

const selectionTabGapWidth = 2

type SelectionTab struct {
	ID    string
	Label string
}

func TabBarHeight(tabs []SelectionTab, activeIdx int, width int) int {
	return len(TabBarLines(tabs, activeIdx, width))
}

func TabBarLines(tabs []SelectionTab, activeIdx int, width int) []string {
	if len(tabs) == 0 {
		return nil
	}
	if width <= 0 {
		width = 1
	}
	lines := []string{}
	current := ""
	currentWidth := 0
	for idx, tab := range tabs {
		unit := TabUnit(tab.Label, idx == activeIdx)
		unitWidth := tui.DisplayWidth(unit)
		gapWidth := 0
		if current != "" {
			gapWidth = selectionTabGapWidth
		}
		if current != "" && currentWidth+gapWidth+unitWidth > width {
			lines = append(lines, current)
			current = ""
			currentWidth = 0
			gapWidth = 0
		}
		if current != "" {
			current += "  "
			currentWidth += gapWidth
		}
		current += unit
		currentWidth += unitWidth
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func TabUnit(label string, active bool) string {
	if active {
		return "[" + label + "]"
	}
	return label
}
