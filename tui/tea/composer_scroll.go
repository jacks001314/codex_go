package tea

import (
	"strings"
	"unicode"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/textarea.rs::scroll_mouse and
// app/owned_transcript.rs (#50109). The owned-screen composer grows with the
// draft, caps itself at two thirds of the screen (with an eight-row floor when
// it fits), and lets the wheel browse the prompt without moving the caret.

const composerWheelRows = 3

// composerBounded reports whether the composer uses the owned-screen height
// and scrolling rules. Inline mode keeps the terminal-owned layout: finalized
// output stays in scrollback and the composer remains a fixed three rows.
func (m *Model) composerBounded() bool {
	return m != nil && m.ownedScreen && !m.rawOutput
}

// composerContentRows returns the number of wrapped prompt rows Bubbles will
// paint for the current draft. It ports the textarea's wrap pass so the model
// can reserve the same height and locate the caret row without reading the
// widget's private viewport.
func (m *Model) composerContentRows() int {
	if m == nil {
		return defaultComposerHeight
	}
	value := m.composer.Value()
	if value == "" {
		return defaultComposerHeight
	}
	width := m.composer.Width()
	rows := 0
	for _, line := range strings.Split(value, "\n") {
		rows += composerWrappedRowCount([]rune(line), width)
	}
	return max(rows, defaultComposerHeight)
}

// composerTargetRows returns the textarea height reserved below the
// transcript. The cap covers the whole composer, including the painted border,
// matching Rust's ComposerRenderOptions::max_height.
func (m *Model) composerTargetRows() int {
	if m == nil || !m.composerBounded() {
		return defaultComposerHeight
	}
	content := m.composerContentRows()
	maxRows := max(m.height*2/3, 8)
	if maxRows > m.height {
		maxRows = m.height
	}
	if m.regionChromeEnabled() {
		maxRows -= 2
	}
	maxRows = max(maxRows, defaultComposerHeight)
	return min(content, maxRows)
}

// composerSectionRows includes the rounded border painted by
// renderComposerRegion.
func (m *Model) composerSectionRows() int {
	rows := m.composerTargetRows()
	if m.regionChromeEnabled() {
		rows += 2
	}
	return rows
}

// resetComposerFollow resumes caret following after editor input. Manual wheel
// browsing detaches only until the next key, paste, or external draft edit.
func (m *Model) resetComposerFollow() {
	if m == nil {
		return
	}
	m.composerManual = false
}

// syncComposerFollowState detects edits made outside Bubble Tea's key path
// (external editor, history recall, command mutations) and resumes following.
func (m *Model) syncComposerFollowState() {
	if m == nil {
		return
	}
	value := m.composer.Value()
	cursor := m.composerCursorKey()
	if value != m.composerLastValue || cursor != m.composerLastCursor {
		if value != m.composerLastValue {
			m.composerSelection.clear()
		}
		m.composerManual = false
		m.composerLastValue = value
		m.composerLastCursor = cursor
	}
}

func (m *Model) composerCursorKey() int {
	if m == nil {
		return 0
	}
	info := m.composer.LineInfo()
	return m.composer.Line()<<16 | (info.StartColumn + info.ColumnOffset)
}

// composerCursorVisualRow translates the textarea's logical line and wrapped
// row offset into a row in the complete prompt.
func (m *Model) composerCursorVisualRow() int {
	if m == nil {
		return 0
	}
	lines := strings.Split(m.composer.Value(), "\n")
	line := m.composer.Line()
	if line < 0 {
		return 0
	}
	if line > len(lines) {
		line = len(lines)
	}
	row := 0
	for i := 0; i < line; i++ {
		row += composerWrappedRowCount([]rune(lines[i]), m.composer.Width())
	}
	return row + m.composer.LineInfo().RowOffset
}

// effectiveComposerScroll clamps the stored scroll offset, then, unless the
// user is browsing manually, keeps the caret inside the visible window.
func (m *Model) effectiveComposerScroll(total int, target int) int {
	maxScroll := max(total-target, 0)
	scroll := clampModalInt(m.composerScroll, 0, maxScroll)
	if !m.composerManual {
		cursor := m.composerCursorVisualRow()
		switch {
		case cursor < scroll:
			scroll = cursor
		case cursor >= scroll+target:
			scroll = cursor - target + 1
		}
	}
	scroll = clampModalInt(scroll, 0, maxScroll)
	m.composerScroll = scroll
	return scroll
}

// handleComposerWheel consumes wheel events over a scrollable owned-screen
// prompt. The transcript, its selection/search/activity focus, and the caret
// all stay put.
func (m *Model) handleComposerWheel(msg bubbletea.MouseMsg) bool {
	if m == nil || !m.composerBounded() || m.composerTopRow < 0 {
		return false
	}
	if msg.Action != bubbletea.MouseActionPress {
		return false
	}
	if msg.Button != bubbletea.MouseButtonWheelUp && msg.Button != bubbletea.MouseButtonWheelDown {
		return false
	}
	if msg.Alt || msg.Ctrl || msg.Shift {
		return false
	}
	if msg.Y < m.composerTopRow || msg.Y >= m.composerTopRow+max(m.composerRows, 1) {
		return false
	}
	total := m.composerContentRows()
	target := m.composerTargetRows()
	if total <= target {
		return false
	}
	scroll := m.effectiveComposerScroll(total, target)
	delta := composerWheelRows
	if msg.Button == bubbletea.MouseButtonWheelUp {
		delta = -composerWheelRows
	}
	next := clampModalInt(scroll+delta, 0, total-target)
	if next != scroll {
		m.composerScroll = next
		m.composerManual = true
	}
	return true
}

// renderComposerWindow paints the complete prompt through the textarea widget
// and crops the current window. Rendering the whole draft keeps Bubbles'
// wrapping, cursor glyph, and styles intact while the model owns the scroll
// offset independently of the caret.
func (m *Model) renderComposerWindow() string {
	if m == nil {
		return ""
	}
	if !m.composerBounded() {
		return m.composer.View()
	}
	m.composerPromptWidth = codextui.DisplayWidth(m.composer.Prompt)
	m.syncComposerFollowState()
	total := m.composerContentRows()
	target := m.composerTargetRows()
	scroll := m.effectiveComposerScroll(total, target)
	if m.composer.MaxHeight < total {
		m.composer.MaxHeight = total
	}
	m.composer.SetHeight(total)
	full := m.composer.View()
	m.composer.SetHeight(target)
	rows := strings.Split(strings.TrimSuffix(full, "\n"), "\n")
	if len(rows) < total {
		total = len(rows)
		scroll = clampModalInt(scroll, 0, max(total-target, 0))
	}
	visible := make([]string, 0, target)
	for _, row := range rows[scroll:min(scroll+target, len(rows))] {
		visible = append(visible, row)
	}
	for len(visible) < target {
		visible = append(visible, "")
	}
	m.applyComposerSelectionHighlight(visible, scroll)
	m.annotateComposerWindowHyperlinks(visible, scroll)
	if scroll > 0 {
		visible[0] = appendComposerScrollArrow(visible[0], "↑")
	}
	if scroll+target < total {
		visible[len(visible)-1] = appendComposerScrollArrow(visible[len(visible)-1], "↓")
	}
	return strings.Join(visible, "\n")
}

func appendComposerScrollArrow(line string, arrow string) string {
	return line + lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(" "+arrow)
}

// composerWrappedRowCount mirrors Bubbles textarea.wrap. Keeping the count in
// lockstep matters because Bubbles renders prompt rows before the model crops
// the visible window.
func composerWrappedRowCount(runes []rune, width int) int {
	return len(composerWrappedRows(runes, width))
}

// composerWrappedRows ports Bubbles textarea.wrap and returns each painted row.
// Selection mapping consumes these rows in order to recover source rune ranges.
func composerWrappedRows(runes []rune, width int) [][]rune {
	if width <= 0 {
		width = 1
	}
	var (
		lines  = [][]rune{{}}
		word   = []rune{}
		row    int
		spaces int
	)
	for _, r := range runes {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			word = append(word, r)
		}
		if spaces > 0 {
			if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces > width {
				row++
				lines = append(lines, []rune{})
			}
			lines[row] = append(lines[row], word...)
			lines[row] = append(lines[row], composerRepeatSpaces(spaces)...)
			spaces = 0
			word = nil
			continue
		}
		if len(word) == 0 {
			continue
		}
		lastWidth := runewidth.RuneWidth(word[len(word)-1])
		if uniseg.StringWidth(string(word))+lastWidth > width {
			if len(lines[row]) > 0 {
				row++
				lines = append(lines, []rune{})
			}
			lines[row] = append(lines[row], word...)
			word = nil
		}
	}
	if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces >= width {
		lines = append(lines, []rune{})
		row++
	}
	lines[row] = append(lines[row], word...)
	lines[row] = append(lines[row], composerRepeatSpaces(spaces+1)...)
	return lines
}

func composerRepeatSpaces(n int) []rune {
	if n <= 0 {
		return nil
	}
	return []rune(strings.Repeat(" ", n))
}
