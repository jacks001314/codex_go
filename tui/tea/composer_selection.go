package tea

import (
	"strings"
	"unicode"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// Rust parity: bottom_pane/chat_composer/mouse.rs and app/owned_transcript.rs
// (#53753). Editable selections follow the same copy-on-select policy as the
// transcript and footer, remain highlighted after copying, and are replaced by
// the next edit.

type composerTextSelection struct {
	active     bool
	dragging   bool
	anchorRune int
	focusRune  int
}

func (s *composerTextSelection) clear() {
	*s = composerTextSelection{}
}

type composerVisualRow struct {
	startRune int
	endRune   int
	text      []rune
}

// composerVisualRows maps every painted prompt row back to source rune offsets.
// Bubbles' wrap output is a partition of the logical line plus one artificial
// trailing space, so consuming rows in order recovers the editable ranges.
func (m *Model) composerVisualRows() []composerVisualRow {
	if m == nil {
		return nil
	}
	value := m.composer.Value()
	lines := strings.Split(value, "\n")
	width := m.composer.Width()
	rows := []composerVisualRow{}
	globalRune := 0
	for lineIndex, line := range lines {
		source := []rune(line)
		consumed := 0
		for _, wrapped := range composerWrappedRows(source, width) {
			take := min(len(wrapped), max(len(source)-consumed, 0))
			text := append([]rune(nil), source[consumed:consumed+take]...)
			rows = append(rows, composerVisualRow{
				startRune: globalRune + consumed,
				endRune:   globalRune + consumed + take,
				text:      text,
			})
			consumed += take
		}
		if consumed < len(source) {
			rows = append(rows, composerVisualRow{
				startRune: globalRune + consumed,
				endRune:   globalRune + len(source),
				text:      append([]rune(nil), source[consumed:]...),
			})
		}
		globalRune += len(source)
		if lineIndex < len(lines)-1 {
			globalRune++
		}
	}
	return rows
}

func (m *Model) composerPositionAt(x int, y int) (int, bool) {
	if m == nil || m.composerTextTopRow < 0 || !m.composerBounded() {
		return 0, false
	}
	rowIndex := y - m.composerTextTopRow
	target := m.composerTargetRows()
	if rowIndex < 0 || rowIndex >= target {
		return 0, false
	}
	rows := m.composerVisualRows()
	if len(rows) == 0 {
		return 0, false
	}
	globalRow := m.composerScroll + rowIndex
	if globalRow >= len(rows) {
		globalRow = len(rows) - 1
	}
	row := rows[globalRow]
	column := x - m.composerTextLeft - m.composerPromptWidth
	if column <= 0 {
		return row.startRune, true
	}
	used := 0
	for index, r := range row.text {
		width := codextui.DisplayWidth(string(r))
		if column < used+width {
			return row.startRune + index, true
		}
		used += width
	}
	return row.endRune, true
}

func (m *Model) handleComposerSelectionMouse(msg bubbletea.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || !m.composerBounded() || m.composerTextTopRow < 0 {
		return false, nil
	}
	if msg.Alt || msg.Ctrl || msg.Shift {
		return false, nil
	}
	switch msg.Action {
	case bubbletea.MouseActionPress:
		if msg.Button == bubbletea.MouseButtonRight {
			if m.composerSelection.active {
				if _, inside := m.composerPositionAt(msg.X, msg.Y); inside {
					if m.clipboardBusy {
						m.notice = "Copy already in progress; try again after it finishes"
						return true, nil
					}
					cmd := m.copyComposerSelection()
					m.composerRightClickCopy = true
					return true, cmd
				}
				m.composerSelection.clear()
			}
			// Without a selection under the pointer, leave the press to the
			// right-click paste fallback.
			return false, nil
		}
		if msg.Button != bubbletea.MouseButtonLeft {
			if m.composerSelection.active {
				m.composerSelection.clear()
				return true, nil
			}
			return false, nil
		}
		position, ok := m.composerPositionAt(msg.X, msg.Y)
		if !ok {
			if m.composerSelection.active {
				m.composerSelection.clear()
			}
			return false, nil
		}
		m.composerSelection = composerTextSelection{
			active:     true,
			dragging:   true,
			anchorRune: position,
			focusRune:  position,
		}
		m.composerRightClickCopy = false
		return true, nil
	case bubbletea.MouseActionMotion:
		if !m.composerSelection.active || !m.composerSelection.dragging {
			return false, nil
		}
		m.extendComposerSelection(msg.X, msg.Y)
		return true, nil
	case bubbletea.MouseActionRelease:
		if msg.Button != bubbletea.MouseButtonLeft ||
			!m.composerSelection.active ||
			!m.composerSelection.dragging {
			return false, nil
		}
		m.extendComposerSelection(msg.X, msg.Y)
		m.composerSelection.dragging = false
		if m.composerSelection.anchorRune == m.composerSelection.focusRune {
			m.composerSelection.clear()
			return true, nil
		}
		if m.copyOnSelectEnabled() {
			return true, m.copyComposerSelection()
		}
		return true, nil
	default:
		return false, nil
	}
}

func (m *Model) extendComposerSelection(x int, y int) {
	if m == nil {
		return
	}
	target := m.composerTargetRows()
	top := m.composerTextTopRow
	bottom := top + target - 1
	y = clampModalInt(y, top, bottom)
	x = max(x, m.composerTextLeft)
	if position, ok := m.composerPositionAt(x, y); ok {
		m.composerSelection.focusRune = position
	}
}

func (m *Model) composerSelectionRange() (int, int, bool) {
	if m == nil || !m.composerSelection.active {
		return 0, 0, false
	}
	start := m.composerSelection.anchorRune
	end := m.composerSelection.focusRune
	if start > end {
		start, end = end, start
	}
	return start, end, start != end
}

func (m *Model) composerSelectedText() string {
	start, end, ok := m.composerSelectionRange()
	if !ok {
		return ""
	}
	value := m.composer.Value()
	startByte := runeIndexToByteOffset(value, start)
	endByte := runeIndexToByteOffset(value, end)
	return value[startByte:endByte]
}

func (m *Model) copyComposerSelection() bubbletea.Cmd {
	text := m.composerSelectedText()
	if text == "" {
		return nil
	}
	m.composerRightClickCopy = false
	return m.clipboardWriteCmd("composer selection", text, false, false)
}

// applyComposerSelectionKey makes the next edit replace the editable selection.
// Non-edit keys cancel the highlight and continue through normal dispatch.
func (m *Model) applyComposerSelectionKey(key bubbletea.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil || !m.composerSelection.active {
		return false, nil
	}
	switch {
	case key.Type == bubbletea.KeyEsc:
		m.composerSelection.clear()
		return true, nil
	case key.Type == bubbletea.KeyRunes && !key.Alt && len(key.Runes) > 0 &&
		(key.Paste || !unicode.IsControl(key.Runes[0])):
		m.replaceComposerSelection(string(key.Runes))
		return true, nil
	case key.Type == bubbletea.KeyBackspace || key.Type == bubbletea.KeyDelete:
		m.replaceComposerSelection("")
		return true, nil
	case key.Type == bubbletea.KeyEnter || key.Type == bubbletea.KeyCtrlJ:
		m.replaceComposerSelection("\n")
		return true, nil
	default:
		m.composerSelection.clear()
		return false, nil
	}
}

func (m *Model) replaceComposerSelection(replacement string) bool {
	start, end, ok := m.composerSelectionRange()
	if !ok {
		m.composerSelection.clear()
		return false
	}
	value := m.composer.Value()
	startByte := runeIndexToByteOffset(value, start)
	endByte := runeIndexToByteOffset(value, end)
	next := value[:startByte] + replacement + value[endByte:]
	m.composerSelection.clear()
	m.setComposerCursorRuneOffset(next, start+len([]rune(replacement)))
	m.resetComposerFollow()
	return true
}

func (m *Model) setComposerCursorRuneOffset(value string, runeOffset int) {
	if m == nil {
		return
	}
	targetLine, targetColumn := lineColumnForRuneOffset(value, runeOffset)
	m.composer.SetValue(value)
	line := m.composer.Line()
	for line > targetLine {
		m.composer.CursorUp()
		line--
	}
	for line < targetLine {
		m.composer.CursorDown()
		line++
	}
	m.composer.SetCursor(targetColumn)
}

func (m *Model) applyComposerSelectionHighlight(rows []string, scroll int) {
	start, end, ok := m.composerSelectionRange()
	if !ok || len(rows) == 0 {
		return
	}
	visualRows := m.composerVisualRows()
	for index := range rows {
		globalRow := scroll + index
		if globalRow < 0 || globalRow >= len(visualRows) {
			continue
		}
		row := visualRows[globalRow]
		overlapStart := max(start, row.startRune)
		overlapEnd := min(end, row.endRune)
		if overlapEnd <= overlapStart {
			continue
		}
		from := m.composerPromptWidth + codextui.DisplayWidth(string(row.text[:overlapStart-row.startRune]))
		to := m.composerPromptWidth + codextui.DisplayWidth(string(row.text[:overlapEnd-row.startRune]))
		rows[index] = highlightDisplayCells(rows[index], from, to)
	}
}

func runeIndexToByteOffset(text string, runeOffset int) int {
	if runeOffset <= 0 {
		return 0
	}
	index := 0
	for byteOffset := range text {
		if index == runeOffset {
			return byteOffset
		}
		index++
	}
	return len(text)
}

func lineColumnForRuneOffset(text string, runeOffset int) (int, int) {
	line, column := 0, 0
	index := 0
	for _, r := range text {
		if index >= runeOffset {
			break
		}
		if r == '\n' {
			line++
			column = 0
		} else {
			column++
		}
		index++
	}
	return line, column
}
