package tea

import (
	keymsg "codex_go/tui/keymsg"
	mousemsg "codex_go/tui/mousemsg"
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	historycell "codex_go/tui/history_cell"
)

// handleWarningsViewMouse gives an unmodified wheel tick inside the painted
// warning body to the viewer before the transcript sees it. Each tick moves
// three rows (Rust #57271).
func (m *Model) handleWarningsViewMouse(msg mousemsg.MouseMsg) bool {
	if m == nil || m.warningsView == nil || msg.Action != mousemsg.MouseActionPress ||
		msg.Alt || msg.Ctrl || msg.Shift {
		return false
	}
	var rows int
	switch msg.Button {
	case mousemsg.MouseButtonWheelUp:
		rows = -3
	case mousemsg.MouseButtonWheelDown:
		rows = 3
	default:
		return false
	}
	region := m.renderedSelection.regions[renderedRegionWarnings]
	if region.w <= 0 || region.h <= 0 ||
		msg.X < region.x || msg.X >= region.x+region.w ||
		msg.Y < region.y || msg.Y >= region.y+region.h {
		return false
	}
	// Rust's WarningsView::scroll_mouse consumes every wheel gesture inside a
	// scrollable body, including ticks that are already clamped at an edge.
	// Returning the movement result here would leak boundary ticks to the
	// transcript.
	m.warningsView.ScrollRows(rows)
	return m.warningsView.MaxOffset() > 0
}

// Rust parity: codex-rs/tui/src/chatwidget/warnings.rs's `open_warnings` and the
// bottom pane's `WarningsView` hosting (#48205/#48206). The viewer shows the
// retained diagnostics one at a time without replacing the draft.

// openWarningsView builds the viewer from the retained diagnostics the footer
// already counts, hiding the dismissed revisions.
func (m *Model) openWarningsView() {
	if m == nil {
		return
	}
	entries := m.warningDisplay.VisibleEntries(m.retainedWarningCells())
	m.warningsView = bottompane.NewWarningsView(entries)
	m.warningsView.SetDaemonRestartHintAt(m.daemonRestartHintEntry(entries))
	m.warningsFlash = ""
}

// daemonRestartHintEntry mirrors ChatWidget::open_warnings's
// `daemon_restart_hint_at`: a managed local daemon points the restart hint at
// the first retained startup configuration warning (#53173).
func (m *Model) daemonRestartHintEntry(entries []historycell.WarningEntry) int {
	if m == nil || !m.daemonManaged || !m.localDaemonSession {
		return -1
	}
	for index, entry := range entries {
		if m.warningDisplay.IsStartupConfigWarning(entry.ID) {
			return index
		}
	}
	return -1
}

// closeWarningsView applies the viewer's decisions and hides it (the
// `AppEvent::UpdateWarnings` arm).
func (m *Model) closeWarningsView() bubbletea.Cmd {
	if m == nil || m.warningsView == nil {
		return nil
	}
	dismissed, kept := m.warningsView.Close()
	m.warningDisplay.ApplyDecisions(dismissed, kept)
	m.warningDisplay.SyncWarnings(m.retainedWarningCells())
	m.warningsView = nil
	m.warningsFlash = ""
	return nil
}

// retainedWarningCells returns every retained diagnostic source for the viewer
// and footer: the warning-bearing history cells plus the merged startup summary
// (Rust's `warning_entries(transcript_cells)`).
func (m *Model) retainedWarningCells() []historycell.WarningCell {
	if m == nil {
		return nil
	}
	cells := make([]historycell.WarningCell, 0, len(m.warningCells)+1)
	// Rust walks the transcript in order, so the startup cell (which sits above
	// the conversation) contributes its entries before later runtime warnings.
	cells = append(cells, m.startupWarnings)
	cells = append(cells, m.warningCells...)
	return cells
}

// updateWarningsViewKey routes a key to the warnings viewer, mirroring
// `WarningsView::handle_key`: plain `k` keeps and advances, the list actions
// navigate and cancel, and the global open-warnings/copy keys are consulted only
// when no list action matched.
func (m *Model) updateWarningsViewKey(msg keymsg.KeyMsg) bubbletea.Cmd {
	if m == nil || m.warningsView == nil {
		return nil
	}
	keySpec := keySpecFromKeyMsg(msg)
	m.warningsFlash = ""
	// Plain k belongs to this viewer, including when a list action is remapped
	// onto it.
	if keySpec == "k" {
		if m.warningsView.KeepAndNext() {
			return m.closeWarningsView()
		}
		return nil
	}
	action := m.warningsListAction(keySpec)
	if action == "" {
		if m.keyMatches("global", "open_warnings", keySpec) {
			return m.closeWarningsView()
		}
		if m.keyMatches("global", "copy", keySpec) {
			if entry, ok := m.warningsView.CurrentEntry(); ok {
				return m.copyWarningsText(entry.Details)
			}
			return nil
		}
		return nil
	}
	switch action {
	case "move_left":
		m.warningsView.MoveLeft()
	case "move_right":
		m.warningsView.MoveRight()
	case "move_up":
		m.warningsView.MoveUp()
	case "move_down":
		m.warningsView.MoveDown()
	case "page_up":
		m.warningsView.PageUp()
	case "page_down":
		m.warningsView.PageDown()
	case "jump_top":
		m.warningsView.JumpTop()
	case "jump_bottom":
		m.warningsView.JumpBottom()
	case "cancel":
		return m.closeWarningsView()
	}
	return nil
}

func (m *Model) warningsListAction(keySpec string) string {
	for _, action := range []string{
		"move_left", "move_right", "move_up", "move_down",
		"page_up", "page_down", "jump_top", "jump_bottom", "cancel", "accept",
	} {
		if m.keyMatches("list", action, keySpec) {
			return action
		}
	}
	return ""
}

// copyWarningsText mirrors `AppEvent::CopyWarning`: the diagnostic's full text
// is copied as plain text and the viewer shows the result.
func (m *Model) copyWarningsText(text string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.clipboardWrite == nil {
		m.warningsFlash = "Copy unavailable"
		return nil
	}
	return m.clipboardWriteCmd("warning", text, false, true)
}

// warningsHints resolves the footer labels from the current keymap (Rust
// resolves the same hints at render time).
func (m *Model) warningsHints() bottompane.WarningsHints {
	label := func(context string, action string) string {
		if m == nil {
			return ""
		}
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, context, action)
		if len(bindings) == 0 {
			return ""
		}
		return codextui.KeybindingDisplayLabel(strings.TrimSpace(bindings[0]))
	}
	cancel := label("list", "cancel")
	if strings.TrimSpace(cancel) == "" {
		cancel = "ctrl+c"
	}
	parts := make([]string, 0, 2)
	for _, action := range []string{"move_left", "move_right"} {
		if part := label("list", action); part != "" {
			parts = append(parts, part)
		}
	}
	scrollUp := label("list", "move_up")
	if strings.TrimSpace(scrollUp) == "" {
		scrollUp = "\u2191"
	}
	scrollDown := label("list", "move_down")
	if strings.TrimSpace(scrollDown) == "" {
		scrollDown = "\u2193"
	}
	return bottompane.WarningsHints{
		Keep:       "k",
		Cancel:     cancel,
		Copy:       label("global", "copy"),
		Navigation: strings.Join(parts, "/"),
		ScrollUp:   scrollUp,
		ScrollDown: scrollDown,
	}
}

// renderWarningsView draws the viewer, which occupies the bottom pane in place
// of the composer while it is open.
func (m *Model) renderWarningsView(width int) string {
	if m == nil || m.warningsView == nil {
		return ""
	}
	height := 13
	if m.height > 0 && m.height-2 < height {
		height = m.height - 2
	}
	if height < 3 {
		height = 3
	}
	lines := m.warningsView.RenderLines(width, height, m.warningsHints())
	if len(lines) == 0 {
		return ""
	}
	if strings.TrimSpace(m.warningsFlash) != "" {
		lines[len(lines)-1] = m.warningsFlash
	}
	return strings.Join(lines, "\n")
}
