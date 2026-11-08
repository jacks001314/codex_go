package tea

import (
	bubbletea "charm.land/bubbletea/v2"
	mousemsg "codex_go/tui/mousemsg"
	"github.com/charmbracelet/x/ansi"
)

// Mouse interaction for the side-by-side conversation layout (Rust #59527):
// dragging the divider previews a width and saves it on release, while a click
// in the unfocused pane moves focus there.

type sidePaneDrag struct {
	active bool
}

// sidebarStripColumn maps a terminal column onto the sidebar pane's own column
// space. The strip only owns columns inside the sidebar pane; the divider and
// the parent pane report false.
func (m *Model) sidebarStripColumn(x int) (int, bool) {
	left, _, _, ok := m.sidePaneLayout(firstPositive(m.width, defaultWidth))
	if !ok {
		return 0, false
	}
	dividerEnd := left + ansi.StringWidth(sideWorkspaceDivider) - 1
	if x <= dividerEnd {
		return 0, false
	}
	return x - dividerEnd - 1, true
}

// handleSidebarTabStripMouse hit-tests the strip exactly as it paints
// (Rust #59928 sidebar_tab_strip.rs hit): motion highlights the tab under the
// pointer, a left press selects that tab, and a press on " + " opens the
// chooser on the new-chat row. It runs before the side-pane guards because a
// first side chat has no active side conversation yet.
func (m *Model) handleSidebarTabStripMouse(msg mousemsg.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || m.State == nil || m.sidebarTabStripTop < 0 || !m.sidebarTabStripVisible() {
		return false, nil
	}
	// The pointer row is measured against the row the strip was painted on, so a
	// banner or inset above the sidebar cannot shift the hit targets.
	if msg.Y != m.sidebarTabStripTop {
		return false, nil
	}
	column, inside := m.sidebarStripColumn(msg.X)
	if !inside {
		return false, nil
	}
	window := m.sidebarTabStripWindow()
	switch msg.Action {
	case mousemsg.MouseActionMotion:
		return m.hoverSidebarTab(column), nil
	case mousemsg.MouseActionPress:
		if msg.Button != mousemsg.MouseButtonLeft {
			return false, nil
		}
		if window.addAt(column) {
			return true, m.openSidebarChooser()
		}
		if index := window.tabAt(column); index >= 0 {
			return true, m.selectSidebarTab(index)
		}
	}
	return false, nil
}

// selectSidebarTab reopens the tab the pointer picked and leaves the chooser
// when it was open (Rust #59928 paints the tabs as click targets).
func (m *Model) selectSidebarTab(index int) bubbletea.Cmd {
	tabs := m.sidebarTabsFor(m.sidebarParentThreadID())
	if index < 0 || index >= len(tabs) || tabs[index] == nil {
		return nil
	}
	if m.activeSide == tabs[index] && m.activeSide.ShowingSide && !m.sidebarChooserOpen {
		return nil
	}
	m.sidebarTabHover = -1
	return batchSideCommands(m.closeSidebarChooser(), m.activateRetainedSideConversation(tabs[index], ""))
}

func (m *Model) handleSidePaneMouse(msg mousemsg.MouseMsg) (bool, bubbletea.Cmd) {
	if handled, cmd := m.handleSidebarTabStripMouse(msg); handled {
		return true, cmd
	}
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
	case mousemsg.MouseActionPress:
		if msg.Button != mousemsg.MouseButtonLeft {
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
	case mousemsg.MouseActionMotion:
		if m.sideDrag == nil || !m.sideDrag.active {
			return false, nil
		}
		width := sidebarColumns(m.width-msg.X-dividerWidth, m.width)
		if m.sideResize == nil || m.sideResize.previewWidth != width {
			m.sideResize = &sidePaneResize{previewWidth: width}
		}
		return true, nil
	case mousemsg.MouseActionRelease:
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
