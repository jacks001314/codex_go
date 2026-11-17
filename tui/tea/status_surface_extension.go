package tea

// Extension status content enters the live footer through the same
// StatusLine height/render surface as the native span line (Rust #60031). A
// provider response prepares a UI tree once; every frame only measures and
// paints it at the current width, so resizes reflow claims instead of clipping
// a cached single row.

import (
	"strings"

	"charm.land/lipgloss/v2"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// SetExtensionStatusLine installs prepared provider status content. Passing nil
// restores the native configured status line.
func (m *Model) SetExtensionStatusLine(line *bottompane.StatusLine) {
	if m == nil {
		return
	}
	if line == nil {
		m.statusLineExtension = nil
	} else {
		copy := *line
		m.statusLineExtension = &copy
	}
	m.refreshTranscript()
}

// ClearExtensionStatusLine drops provider content and restores the configured
// native status line.
func (m *Model) ClearExtensionStatusLine() {
	m.SetExtensionStatusLine(nil)
}

// extensionStatusLineRows measures and paints extension content inside the
// status viewport. The status row budget caps the height exactly like the
// native wrapping path.
func (m *Model) extensionStatusLineRows(width int, budget int, focus bottompane.StatusLineFocus) []bottompane.StatusLineRow {
	if m == nil || m.statusLineExtension == nil || width < 1 || budget < 1 {
		return nil
	}
	accent := func(value bottompane.StatusLineAccent) string {
		return statusLineAccentSGR(m.tuiTheme, value)
	}
	return m.statusLineExtension.RenderRows(width, budget, focus, bottompane.StatusLineRenderOptions{
		ThreadColorSGR: m.statusLineThreadColorSGR(),
		AccentColorSGR: accent,
		ThemeID:        m.tuiTheme,
		ColorLevel:     codextui.DetectStdoutColorLevel(),
	})
}

// extensionStatusLineHeight reports how many rows extension content claims at
// width, capped by the footer budget.
func (m *Model) extensionStatusLineHeight(width int, budget int) int {
	if m == nil || m.statusLineExtension == nil || width < 1 || budget < 1 {
		return 0
	}
	return min(m.statusLineExtension.Height(width, budget), budget)
}

// renderExtensionStatusSection paints extension rows with the footer indent and
// the plan-mode indicator on the bottom row, mirroring Rust's right-aligned
// mode indicator beside the status surface.
func (m *Model) renderExtensionStatusSection() string {
	width := max(m.width-2, 1)
	budget := m.statusLineRowBudget()
	focus := bottompane.StatusLineFocused
	contentWidth := m.extensionStatusLineContentWidth()
	rows := m.extensionStatusLineRows(contentWidth, budget, focus)
	if len(rows) == 0 {
		if label := m.extensionStatusIndicatorLabel(width); label != "" {
			padded := strings.Repeat(" ", max(width-lipgloss.Width(label), 0)) + label
			rows = []bottompane.StatusLineRow{{Plain: padded, Styled: padded}}
		} else {
			return ""
		}
	} else if label := m.extensionStatusIndicatorLabel(width); label != "" {
		last := len(rows) - 1
		plainWidth := codextui.DisplayWidth(rows[last].Plain)
		labelWidth := lipgloss.Width(label)
		padding := max(width-plainWidth-labelWidth, 2)
		rows[last].Plain += strings.Repeat(" ", padding) + label
		rows[last].Styled += strings.Repeat(" ", padding) + label
	}
	for index := range rows {
		rows[index].Styled = "  " + rows[index].Styled
	}
	return m.footerStyle.Render(joinStatusLineRows(rows))
}

// extensionStatusLineContentWidth is the width left for extension content after
// the plan-mode indicator and its two-column gap. Measurement and painting must
// use the same width or the reserved rows can disagree with the rendered rows.
func (m *Model) extensionStatusLineContentWidth() int {
	width := max(m.width-2, 1)
	label := m.extensionStatusIndicatorLabel(width)
	if label == "" {
		return width
	}
	return max(width-lipgloss.Width(label)-2, 1)
}

// extensionStatusIndicatorLabel selects the full plan-mode cycle hint while it
// still leaves readable status content, and the compact label otherwise.
func (m *Model) extensionStatusIndicatorLabel(width int) string {
	labels := m.planStatusIndicatorLabels()
	if len(labels) == 0 {
		return ""
	}
	full := labels[0]
	if lipgloss.Width(full)+8 <= width {
		return full
	}
	return labels[len(labels)-1]
}

func joinStatusLineRows(rows []bottompane.StatusLineRow) string {
	values := make([]string, 0, len(rows))
	for _, row := range rows {
		values = append(values, row.Styled)
	}
	return strings.Join(values, "\n")
}
