package tea

import (
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Mouse interaction for the side-by-side conversation layout (Rust #59527):
// dragging the divider previews a width and saves it on release, while a click
// in the unfocused pane moves focus there.

type sidePaneDrag struct {
	active bool
}

func (m *Model) handleSidePaneMouse(msg bubbletea.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || m.State == nil || m.activeSide == nil || !m.sideWorkspaceEnabled() || m.sideZoomed {
		return false, nil
	}
	left, right, _, ok := m.sidePaneLayout(m.width)
	if !ok {
		return false, nil
	}
	if msg.Y < 0 || msg.Y >= m.transcript.Height {
		return false, nil
	}
	dividerWidth := ansi.StringWidth(sideWorkspaceDivider)
	dividerStart := left
	dividerEnd := left + dividerWidth - 1
	// Rust #59527 scrolls whichever pane the wheel is over: the focused pane
	// through its transcript viewport and the inactive pane through its own
	// parked offset.
	if rows, wheel := m.transcriptWheelRows(msg); wheel {
		if rows == 0 {
			return true, nil
		}
		focusedRight := m.activeSide.ShowingSide
		if (msg.X > dividerEnd) == focusedRight {
			m.scrollTranscriptRows(rows)
			return true, nil
		}
		// The inactive pane scrolls its own window; the first row it paints is
		// clamped to the rows the last frame reported.
		offset, saved, maxFirst := &m.activeSide.ParentScroll, &m.activeSide.ParentInteractionSaved, m.activeSide.ParentViewMax
		if !focusedRight {
			offset, saved, maxFirst = &m.activeSide.SideScroll, &m.activeSide.SideInteractionSaved, m.activeSide.SideViewMax
		}
		if !*saved {
			*offset, *saved = maxFirst, true
		}
		*offset = clampViewportOffset(*offset+rows, 0, maxFirst)
		return true, nil
	}
	switch msg.Action {
	case bubbletea.MouseActionPress:
		if msg.Button != bubbletea.MouseButtonLeft {
			return false, nil
		}
		if msg.X >= dividerStart && msg.X <= dividerEnd {
			m.sideDrag = &sidePaneDrag{active: true}
			m.sideResize = &sidePaneResize{previewWidth: right}
			return true, nil
		}
		if msg.X > dividerEnd {
			if !m.activeSide.ShowingSide {
				return true, m.focusSidebarContent()
			}
			return false, nil
		}
		if msg.X < dividerStart && m.activeSide.ShowingSide {
			return true, m.focusMainComposer()
		}
	case bubbletea.MouseActionMotion:
		if m.sideDrag == nil || !m.sideDrag.active {
			return false, nil
		}
		width := sidebarColumns(m.width-msg.X-dividerWidth, m.width)
		if m.sideResize == nil || m.sideResize.previewWidth != width {
			m.sideResize = &sidePaneResize{previewWidth: width}
		}
		return true, nil
	case bubbletea.MouseActionRelease:
		if m.sideDrag == nil {
			return false, nil
		}
		m.sideDrag = nil
		if m.sideResize == nil {
			return true, nil
		}
		width := sidebarColumns(m.sideResize.previewWidth, m.width)
		m.sideResize = nil
		m.sideExactWidth = sideExactWidth{total: m.width, width: width, valid: true}
		m.sideWidthPercent = widthPercent(width, m.width)
		return true, m.saveConfigPanelPreference("tui.sidebar_width_percent", m.sideWidthPercent)
	}
	return false, nil
}
