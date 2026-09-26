package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	historycell "codex_go/tui/history_cell"
)

// Rust parity: codex-rs/tui/src/chatwidget/warnings.rs's `open_warnings` and the
// bottom pane's `WarningsView` hosting (#48205/#48206). The viewer shows the
// retained diagnostics one at a time without replacing the draft.

// openWarningsView builds the viewer from the retained diagnostics the footer
// already counts, hiding the dismissed revisions.
func (m *Model) openWarningsView() {
	if m == nil {
		return
	}
	entries := m.warningDisplay.VisibleEntries([]historycell.WarningCell{m.startupWarnings})
	m.warningsView = bottompane.NewWarningsView(entries)
	m.warningsFlash = ""
}

// closeWarningsView applies the viewer's decisions and hides it (the
// `AppEvent::UpdateWarnings` arm).
func (m *Model) closeWarningsView() bubbletea.Cmd {
	if m == nil || m.warningsView == nil {
		return nil
	}
	dismissed, kept := m.warningsView.Close()
	m.warningDisplay.ApplyDecisions(dismissed, kept)
	m.warningDisplay.SyncWarnings([]historycell.WarningCell{m.startupWarnings})
	m.warningsView = nil
	m.warningsFlash = ""
	return nil
}

// updateWarningsViewKey routes a key to the warnings viewer, mirroring
// `WarningsView::handle_key`: plain `k` keeps and advances, the list actions
// navigate and cancel, and the global open-warnings/copy keys are consulted only
// when no list action matched.
func (m *Model) updateWarningsViewKey(msg bubbletea.KeyMsg) bubbletea.Cmd {
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
				m.copyWarningsText(entry.Details)
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
func (m *Model) copyWarningsText(text string) {
	if m == nil {
		return
	}
	if m.clipboardWrite == nil {
		m.warningsFlash = "Copy unavailable"
		return
	}
	if err := m.clipboardWrite(text); err != nil {
		m.warningsFlash = "Copy failed"
		return
	}
	m.warningsFlash = "Copied"
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
	return bottompane.WarningsHints{
		Keep:       "k",
		Cancel:     cancel,
		Copy:       label("global", "copy"),
		Navigation: strings.Join(parts, "/"),
		Scroll:     label("list", "move_down"),
	}
}

// renderWarningsView draws the viewer, which occupies the bottom pane in place
// of the composer while it is open.
func (m *Model) renderWarningsView(width int) string {
	if m == nil || m.warningsView == nil {
		return ""
	}
	height := 12
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
