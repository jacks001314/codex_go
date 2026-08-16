package tea

import (
	"strings"
	"unicode"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// applyVimModeKey dispatches Vim normal/insert mode keys when /vim is enabled
// (m.vimMode), mirroring Rust's bottom_pane/chat_composer.rs Vim support. In
// normal mode the registered vim_normal actions are handled through the
// resolved keymap: insert-entering (i/a/A/I/o/O/s/C), motions
// (h/l/k/j/w/b/e/0/$), edits (x/D), line yank (Y) + paste (p), and the dd/yy
// line-operator repeats. In insert mode keys type normally until Esc returns
// to normal mode. When Vim mode is off, or a modal/popup is active, the key
// is not handled here.
func (m *Model) applyVimModeKey(msg bubbletea.KeyMsg, keySpec string) bool {
	if m == nil || !m.vimMode || m.modal != nil || m.overlay != nil || m.slashPopup.Active || m.skillPopup.Active {
		return false
	}
	if m.vimInsert {
		if keySpec == "esc" {
			m.vimInsert = false
			m.vimPendingOp = ""
			return true
		}
		// Insert mode types normally.
		return false
	}
	// Normal mode: a pending line operator (d or y) waits for its repeat key.
	if m.vimPendingOp != "" {
		if keySpec == m.vimPendingOp {
			switch m.vimPendingOp {
			case "d":
				m.deleteVimLine()
			case "y":
				m.yankVimLine()
			}
			m.vimPendingOp = ""
			return true
		}
		// A different key cancels the pending operator. The full operator +
		// motion/text-object engine is a tracked follow-up; the key then
		// dispatches normally below.
		m.vimPendingOp = ""
	}
	switch {
	case m.keyMatches("vim_normal", "enter_insert", keySpec):
		m.vimInsert = true
	case m.keyMatches("vim_normal", "append_after_cursor", keySpec):
		m.vimCursorRight()
		m.vimInsert = true
	case m.keyMatches("vim_normal", "append_line_end", keySpec):
		m.composer.CursorEnd()
		m.vimInsert = true
	case m.keyMatches("vim_normal", "insert_line_start", keySpec):
		m.vimCursorLineStartNonBlank()
		m.vimInsert = true
	case m.keyMatches("vim_normal", "open_line_below", keySpec):
		m.composer.CursorEnd()
		m.composer.InsertString("\n")
		m.vimInsert = true
	case m.keyMatches("vim_normal", "open_line_above", keySpec):
		m.composer.CursorStart()
		m.composer.InsertString("\n")
		m.composer.CursorUp()
		m.vimInsert = true
	case m.keyMatches("vim_normal", "substitute_char", keySpec):
		m.deleteVimCharAtCursor()
		m.vimInsert = true
	case m.keyMatches("vim_normal", "change_to_line_end", keySpec):
		m.deleteVimToLineEnd()
		m.vimInsert = true
	case m.keyMatches("vim_normal", "delete_char", keySpec):
		m.deleteVimCharAtCursor()
	case m.keyMatches("vim_normal", "delete_to_line_end", keySpec):
		m.deleteVimToLineEnd()
	case m.keyMatches("vim_normal", "move_left", keySpec):
		m.vimCursorLeft()
	case m.keyMatches("vim_normal", "move_right", keySpec):
		m.vimCursorRight()
	case m.keyMatches("vim_normal", "move_up", keySpec):
		m.composer.CursorUp()
	case m.keyMatches("vim_normal", "move_down", keySpec):
		m.composer.CursorDown()
	case m.keyMatches("vim_normal", "move_word_forward", keySpec):
		m.vimWordMotion(1)
	case m.keyMatches("vim_normal", "move_word_backward", keySpec):
		m.vimWordMotion(-1)
	case m.keyMatches("vim_normal", "move_word_end", keySpec):
		m.vimWordMotion(2)
	case m.keyMatches("vim_normal", "move_line_start", keySpec):
		m.composer.CursorStart()
	case m.keyMatches("vim_normal", "move_line_end", keySpec):
		m.composer.CursorEnd()
	case m.keyMatches("vim_normal", "yank_line", keySpec):
		m.yankVimLine()
	case m.keyMatches("vim_normal", "paste_after", keySpec):
		m.pasteVimAfterCursor()
	case m.keyMatches("vim_normal", "start_delete_operator", keySpec):
		m.vimPendingOp = "d"
	case m.keyMatches("vim_normal", "start_yank_operator", keySpec):
		m.vimPendingOp = "y"
	default:
		return false
	}
	return true
}

// vimCursorColumn returns the cursor column within the current logical line.
// The textarea's LineInfo exposes the wrapped-row start and the column offset
// within that row; their sum is the logical line column (byte-based, matching
// SetCursor's clamping).
func (m *Model) vimCursorColumn() int {
	info := m.composer.LineInfo()
	return info.StartColumn + info.ColumnOffset
}

// vimCurrentLine returns the logical line the cursor is on.
func (m *Model) vimCurrentLine() string {
	lines := strings.Split(m.composer.Value(), "\n")
	row := m.composer.Line()
	if row < 0 || row >= len(lines) {
		return ""
	}
	return lines[row]
}

func (m *Model) vimCursorLeft() {
	if col := m.vimCursorColumn(); col > 0 {
		m.composer.SetCursor(col - 1)
	}
}

func (m *Model) vimCursorRight() {
	col := m.vimCursorColumn()
	if col < len(m.vimCurrentLine()) {
		m.composer.SetCursor(col + 1)
	}
}

func (m *Model) vimCursorLineStartNonBlank() {
	line := m.vimCurrentLine()
	col := 0
	for col < len(line) {
		r, size := utf8.DecodeRuneInString(line[col:])
		if !unicode.IsSpace(r) {
			break
		}
		col += size
	}
	m.composer.SetCursor(col)
}

// vimValueByteOffset returns the byte offset into the composer value of the
// cursor (sum of prior line lengths + newlines + the current column).
func (m *Model) vimValueByteOffset() int {
	lines := strings.Split(m.composer.Value(), "\n")
	offset := 0
	for i := 0; i < m.composer.Line() && i < len(lines); i++ {
		offset += len(lines[i]) + 1
	}
	return offset + m.vimCursorColumn()
}

// deleteVimCharAtCursor removes the rune under the cursor (x / s).
func (m *Model) deleteVimCharAtCursor() {
	value := m.composer.Value()
	offset := m.vimValueByteOffset()
	if offset >= len(value) {
		return
	}
	_, size := utf8.DecodeRuneInString(value[offset:])
	m.composer.SetValue(value[:offset] + value[offset+size:])
	m.composer.SetCursor(offset)
}

// deleteVimToLineEnd removes the text from the cursor to the end of the
// current line, keeping the newline (D / C).
func (m *Model) deleteVimToLineEnd() {
	value := m.composer.Value()
	offset := m.vimValueByteOffset()
	line := m.vimCurrentLine()
	col := m.vimCursorColumn()
	if col >= len(line) {
		return
	}
	next := value[:offset] + value[offset+len(line)-col:]
	m.composer.SetValue(next)
	m.composer.SetCursor(offset)
}

// deleteVimLine removes the whole current line including its newline (dd).
func (m *Model) deleteVimLine() {
	value := m.composer.Value()
	lines := strings.Split(value, "\n")
	row := m.composer.Line()
	if row < 0 || row >= len(lines) {
		return
	}
	offset := 0
	for i := 0; i < row; i++ {
		offset += len(lines[i]) + 1
	}
	end := offset + len(lines[row])
	if row < len(lines)-1 {
		end++
	}
	next := value[:offset] + value[end:]
	m.composer.SetValue(next)
	m.composer.SetCursor(offset)
}

// yankVimLine stores the current line in the Vim yank buffer (Y / yy).
func (m *Model) yankVimLine() {
	m.vimYank = m.vimCurrentLine()
}

// pasteVimAfterCursor inserts the yank buffer after the cursor (p).
func (m *Model) pasteVimAfterCursor() {
	if m.vimYank == "" {
		return
	}
	offset := m.vimValueByteOffset()
	value := m.composer.Value()
	m.composer.SetValue(value[:offset] + m.vimYank + value[offset:])
	m.composer.SetCursor(offset + len(m.vimYank))
}

// vimWordMotion moves the cursor by word boundaries on the current line.
// kind 1 = w (next word start), -1 = b (previous word start), 2 = e (end of
// current or next word). A word is a run of non-space runes (an approximation
// of Vim's word for the composer surface).
func (m *Model) vimWordMotion(kind int) {
	line := m.vimCurrentLine()
	runes := []rune(line)
	byteCol := m.vimCursorColumn()
	from := runeIndexForByte(line, byteCol)
	if from > len(runes) {
		from = len(runes)
	}
	var target int
	switch kind {
	case 1:
		target = vimNextWordStart(runes, from)
	case -1:
		target = vimPrevWordStart(runes, from)
	default:
		target = vimWordEndIndex(runes, from)
	}
	if target < 0 {
		target = 0
	}
	if target > len(runes) {
		target = len(runes)
	}
	m.composer.SetCursor(byteOffsetForRuneIndex(line, target))
}

func runeIndexForByte(s string, byteCol int) int {
	if byteCol <= 0 {
		return 0
	}
	bytePos := 0
	for i, r := range s {
		if bytePos >= byteCol {
			return i
		}
		bytePos += len(string(r))
		if bytePos >= byteCol {
			return i + 1
		}
	}
	return len([]rune(s))
}

func byteOffsetForRuneIndex(s string, runeIndex int) int {
	if runeIndex <= 0 {
		return 0
	}
	bytePos := 0
	for i, r := range s {
		if i >= runeIndex {
			break
		}
		bytePos += len(string(r))
	}
	return bytePos
}

func isVimSpace(r rune) bool {
	return unicode.IsSpace(r)
}

func vimNextWordStart(runes []rune, from int) int {
	i := from
	if i < len(runes) && !isVimSpace(runes[i]) {
		// Move to the end of the current word.
		for i < len(runes) && !isVimSpace(runes[i]) {
			i++
		}
	}
	// Skip spaces to the next word start.
	for i < len(runes) && isVimSpace(runes[i]) {
		i++
	}
	return i
}

func vimPrevWordStart(runes []rune, from int) int {
	i := from
	// Skip spaces backward to the preceding word end.
	for i > 0 && isVimSpace(runes[i-1]) {
		i--
	}
	// Move back to the start of the word we are in.
	for i > 0 && !isVimSpace(runes[i-1]) {
		i--
	}
	return i
}

func vimWordEndIndex(runes []rune, from int) int {
	i := from
	// If the cursor is at the end of a word, advance to the next word.
	if i < len(runes) && !isVimSpace(runes[i]) && (i+1 >= len(runes) || isVimSpace(runes[i+1])) {
		i++
	}
	for i < len(runes) && isVimSpace(runes[i]) {
		i++
	}
	if i >= len(runes) {
		return len(runes)
	}
	for i+1 < len(runes) && !isVimSpace(runes[i+1]) {
		i++
	}
	return i
}
