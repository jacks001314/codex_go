package tea

import (
	bubbletea "charm.land/bubbletea/v2"
	mousemsg "codex_go/tui/mousemsg"
	"github.com/charmbracelet/x/ansi"
)

// Rust side_focus.rs activates a sidebar control only when the pointer presses
// and releases on the same painted target: the down event captures the target
// plus the sidebar input state of the frame that painted it, and the up event
// compares against that capture. A drag onto another tab, a release after the
// pane switched views, or a stray release therefore never fires an action. The
// tab strip and the chooser rows share this one path.

// sidebarTargetKind names a painted sidebar control a pointer can hit.
type sidebarTargetKind int

const (
	sidebarTargetTabCell sidebarTargetKind = iota + 1
	sidebarTargetTabClose
	sidebarTargetTabAdd
	sidebarTargetTabOverflow
	sidebarTargetChooserRow
)

// sidebarSpan is the painted column or row range of a target, which is what
// Rust compares between the press and the release (SidebarPress::area).
type sidebarSpan struct {
	start int
	end   int
}

// sidebarTarget mirrors Rust side_focus.rs SidebarTarget. index is the strip
// cell index (chats first, then previews) or the chooser choice index.
type sidebarTarget struct {
	kind  sidebarTargetKind
	index int
	span  sidebarSpan
}

// sidebarInputState snapshots which sidebar content the frame painted, so a
// release only matches a press made on the same pane state (Rust
// SidebarInputState).
type sidebarInputState struct {
	parent      string
	chooserOpen bool
	document    string
	showingSide bool
}

// sidebarPress is the captured down event Rust keeps in App::sidebar_press.
type sidebarPress struct {
	state  sidebarInputState
	target sidebarTarget
	button mousemsg.MouseButton
}

// sidebarInputStateFor snapshots the sidebar content the current frame paints.
func (m *Model) sidebarInputStateFor() sidebarInputState {
	state := sidebarInputState{}
	if m == nil {
		return state
	}
	state.parent = m.sidebarParentThreadID()
	state.chooserOpen = m.sidebarChooserVisible() && m.sidebarChooserFocused
	state.document = m.sidebarDocumentActive
	if m.activeSide != nil {
		state.showingSide = m.activeSide.ShowingSide
	}
	return state
}

// sidebarPointInPane reports whether a pointer sits inside the sidebar pane
// (the column right of the divider) at a painted transcript row.
func (m *Model) sidebarPointInPane(x int, y int) bool {
	left, right, _, ok := m.sidePaneLayout(firstPositive(m.width, defaultWidth))
	if !ok || right <= 0 {
		return false
	}
	start := left + ansi.StringWidth(sideWorkspaceDivider)
	return x >= start && x < start+right && y >= 0 && y < max(m.transcript.Height, 1)
}

// sidebarTabStripTargetAt resolves a strip column onto the control painted
// there: the trailing "+", an overflow arrow, a tab's close glyph, or the tab
// cell itself (Rust sidebar_tab_strip::TabStrip::targets).
func (m *Model) sidebarTabStripTargetAt(column int) (sidebarTarget, bool) {
	window := m.sidebarTabStripWindow()
	if window.addAt(column) {
		return sidebarTarget{
			kind:  sidebarTargetTabAdd,
			index: -1,
			span:  sidebarSpan{start: window.addStart, end: window.addStart + window.addWidth},
		}, true
	}
	if index := window.overflowAt(column); index >= 0 {
		return sidebarTarget{
			kind:  sidebarTargetTabOverflow,
			index: index,
			span:  sidebarSpan{start: column, end: column + 1},
		}, true
	}
	index, within, ok := window.tabCellAt(column)
	if !ok {
		return sidebarTarget{}, false
	}
	start := column - within
	if within == window.tabWidth-1 && window.tabWidth >= 3 {
		return sidebarTarget{
			kind:  sidebarTargetTabClose,
			index: index,
			span:  sidebarSpan{start: column, end: column + 1},
		}, true
	}
	return sidebarTarget{
		kind:  sidebarTargetTabCell,
		index: index,
		span:  sidebarSpan{start: start, end: start + window.tabWidth},
	}, true
}

// sidebarTargetAt hit-tests the current frame the same way it paints: the tab
// strip first, then the chooser's choice rows.
func (m *Model) sidebarTargetAt(msg mousemsg.MouseMsg) (sidebarTarget, bool) {
	if m == nil || m.State == nil {
		return sidebarTarget{}, false
	}
	if m.sidebarTabStripTop >= 0 && m.sidebarTabStripVisible() && msg.Y == m.sidebarTabStripTop {
		if column, inside := m.sidebarStripColumn(msg.X); inside {
			return m.sidebarTabStripTargetAt(column)
		}
		return sidebarTarget{}, false
	}
	if m.sidebarChooserVisible() && m.sidebarChooserFocused && m.sidebarPointInPane(msg.X, msg.Y) {
		if row := msg.Y - max(m.sidebarTabStripTop, 0); row >= 0 {
			if index, ok := m.sidebarChooserChoiceAtRow(row); ok {
				return sidebarTarget{
					kind:  sidebarTargetChooserRow,
					index: index,
					span:  sidebarSpan{start: msg.Y, end: msg.Y + 1},
				}, true
			}
		}
	}
	return sidebarTarget{}, false
}

// handleSidebarPressMouse mirrors Rust side_focus::handle_sidebar_input for
// mouse down/up: the down records the pressed target and the up activates it
// only when the release lands on the same target in the same pane state.
func (m *Model) handleSidebarPressMouse(msg mousemsg.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || m.State == nil {
		return false, nil
	}
	if msg.Action != mousemsg.MouseActionPress && msg.Action != mousemsg.MouseActionRelease {
		return false, nil
	}
	if msg.Button != mousemsg.MouseButtonLeft && msg.Button != mousemsg.MouseButtonMiddle {
		return false, nil
	}
	down := msg.Action == mousemsg.MouseActionPress
	previous := m.sidebarPress
	m.sidebarPress = nil
	overStrip := m.sidebarTabStripTop >= 0 && m.sidebarTabStripVisible() && msg.Y == m.sidebarTabStripTop
	overChooser := m.sidebarChooserVisible() && m.sidebarChooserFocused && m.sidebarPointInPane(msg.X, msg.Y)
	if !overStrip && !overChooser && previous == nil {
		// Rust side_focus.rs moves the chooser's focus back to the main
		// composer when a press lands outside the panel; the list stays open
		// and keeps its selection.
		if down && msg.Button == mousemsg.MouseButtonLeft && m.sidebarChooserVisible() && m.sidebarChooserFocused {
			m.sidebarChooserFocused = false
			return true, nil
		}
		return false, nil
	}
	var candidate *sidebarPress
	if m.sideResize == nil {
		if target, ok := m.sidebarTargetAt(msg); ok {
			candidate = &sidebarPress{state: m.sidebarInputStateFor(), target: target, button: msg.Button}
		}
	}
	if down {
		m.sidebarPress = candidate
		return true, nil
	}
	if candidate == nil || previous == nil || *previous != *candidate {
		return true, nil
	}
	return true, m.activateSidebarTarget(*previous)
}

// activateSidebarTarget runs the action Rust dispatches for a matched press:
// left closes a close cell, left opens or selects, middle closes a tab.
func (m *Model) activateSidebarTarget(press sidebarPress) bubbletea.Cmd {
	switch press.target.kind {
	case sidebarTargetTabCell:
		if press.button == mousemsg.MouseButtonMiddle {
			return m.closeSidebarTabAtIndex(press.target.index)
		}
		return m.selectSidebarTab(press.target.index)
	case sidebarTargetTabClose:
		return m.closeSidebarTabAtIndex(press.target.index)
	case sidebarTargetTabOverflow:
		return m.selectSidebarTab(press.target.index)
	case sidebarTargetTabAdd:
		if press.button != mousemsg.MouseButtonLeft {
			return nil
		}
		return m.openSidebarChooser()
	case sidebarTargetChooserRow:
		if press.button != mousemsg.MouseButtonLeft {
			return nil
		}
		m.sidebarChoice = press.target.index
		return m.acceptSidebarChooser()
	}
	return nil
}
