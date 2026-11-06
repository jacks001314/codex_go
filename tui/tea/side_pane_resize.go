package tea

import (
	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Pane resize and zoom controls (Rust #59595, codex-rs/tui/src/app/side_pane.rs).
// Widths are kept as a saved percentage plus an exact column allocation for
// the terminal width where the user applied it, so repeated frames cannot drift
// by rounding.

// resetSidePaneViewState clears transient pane layout state when the visible
// conversation pair changes.
func (m *Model) resetSidePaneViewState() {
	if m == nil {
		return
	}
	m.sideZoomed = false
	m.sideResize = nil
}

// sidePaneSplitAvailable reports whether both panes are visible at the current
// width (Rust conversation_focus_available + split layout).
func (m *Model) sidePaneSplitAvailable() bool {
	if m == nil || m.activeSide == nil || !m.sideWorkspaceEnabled() || m.transcriptUnavailableForPanes() {
		return false
	}
	_, _, ok := sideWorkspaceWidthsForPercent(m.width, m.sideWidthPercentValue(), m.sideExactWidth)
	return ok
}

func (m *Model) togglePaneZoom() bubbletea.Cmd {
	if m == nil || m.activeSide == nil || !m.sideWorkspaceEnabled() {
		return nil
	}
	m.sideResize = nil
	m.sideZoomed = !m.sideZoomed
	return nil
}

// setFocusedPaneWidth assigns the focused pane the requested share of the
// split, converting back to the sidebar percentage before saving.
func (m *Model) setFocusedPaneWidth(focusedPercent int) bubbletea.Cmd {
	if m == nil || !m.sidePaneSplitAvailable() {
		return nil
	}
	sidePercent := focusedPercent
	if !m.activeSide.ShowingSide {
		sidePercent = 100 - focusedPercent
	}
	available := max(m.width-ansi.StringWidth(sideWorkspaceDivider), 1)
	preferred := (available*clampModalInt(sidePercent, 0, 100) + 99) / 100
	width := sidebarColumns(preferred, m.width)
	percent := widthPercent(width, m.width)
	m.sideZoomed = false
	m.sideExactWidth = sideExactWidth{total: m.width, width: width, valid: true}
	// Apply the preset immediately so the next frame uses it even before the
	// settings write completes.
	m.sideWidthPercent = percent
	return m.saveConfigPanelPreference("tui.sidebar_width_percent", percent)
}

// nudgeSidePaneWidth moves the divider by a percentage of the terminal width;
// Rust binds this to leader shift-h/shift-l while the side pane has focus.
func (m *Model) nudgeSidePaneWidth(deltaPercent int) bubbletea.Cmd {
	if m == nil || m.activeSide == nil || !m.activeSide.ShowingSide || !m.sidePaneSplitAvailable() {
		return nil
	}
	_, sideWidth, ok := sideWorkspaceWidthsForPercent(m.width, m.sideWidthPercentValue(), m.sideExactWidth)
	if !ok {
		return nil
	}
	step := (m.width*absInt(deltaPercent) + 50) / 100
	if step < 1 {
		step = 1
	}
	next := sidebarColumns(sideWidth+step*signInt(deltaPercent), m.width)
	if next == sideWidth {
		return nil
	}
	m.sideExactWidth = sideExactWidth{}
	m.sideWidthPercent = widthPercent(next, m.width)
	return m.saveConfigPanelPreference("tui.sidebar_width_percent", m.sideWidthPercent)
}

// beginSidePaneResize enters the keyboard resize preview at the current width.
func (m *Model) beginSidePaneResize() bubbletea.Cmd {
	if m == nil || !m.sidePaneSplitAvailable() {
		return nil
	}
	_, sideWidth, ok := sideWorkspaceWidthsForPercent(m.width, m.sideWidthPercentValue(), m.sideExactWidth)
	if !ok {
		return nil
	}
	m.sideZoomed = false
	m.sideResize = &sidePaneResize{previewWidth: sideWidth}
	return nil
}

// handleSidePaneResizeKey consumes input while resize mode is active. Arrow
// keys and h/l move one column, Shift+Left/Right move ten, and Enter or Esc
// saves the preview.
func (m *Model) handleSidePaneResizeKey(message bubbletea.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil || m.sideResize == nil {
		return false, nil
	}
	if !m.sidePaneSplitAvailable() {
		m.sideResize = nil
		return false, nil
	}
	delta := 0
	switch message.Type {
	case bubbletea.KeyLeft:
		delta = 1
	case bubbletea.KeyRight:
		delta = -1
	case bubbletea.KeyShiftLeft:
		delta = 10
	case bubbletea.KeyShiftRight:
		delta = -10
	case bubbletea.KeyEnter, bubbletea.KeyEsc:
		preview := m.sideResize.previewWidth
		m.sideResize = nil
		if preview <= 0 {
			return true, nil
		}
		width := sidebarColumns(preview, m.width)
		m.sideExactWidth = sideExactWidth{total: m.width, width: width, valid: true}
		m.sideWidthPercent = widthPercent(width, m.width)
		return true, m.saveConfigPanelPreference("tui.sidebar_width_percent", m.sideWidthPercent)
	case bubbletea.KeyRunes:
		if len(message.Runes) == 1 {
			switch message.Runes[0] {
			case 'h':
				delta = 1
			case 'l':
				delta = -1
			}
		}
	}
	if delta != 0 {
		next := sidebarColumns(m.sideResize.previewWidth+delta, m.width)
		if next != m.sideResize.previewWidth {
			m.sideResize.previewWidth = next
		}
	}
	return true, nil
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func signInt(value int) int {
	switch {
	case value > 0:
		return 1
	case value < 0:
		return -1
	default:
		return 0
	}
}
