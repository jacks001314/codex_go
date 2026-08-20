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
	// Rust #39661: a pending `r` replacement consumes the next typed
	// character, replacing the grapheme under the cursor while remaining in
	// normal mode. Esc cancels the pending replacement.
	if m.vimPendingReplace {
		m.vimPendingReplace = false
		if keySpec == "esc" {
			return true
		}
		if msg.Type == bubbletea.KeyRunes && len(msg.Runes) == 1 {
			m.replaceVimCharAtCursor(msg.Runes[0])
		} else if keySpec == "enter" {
			m.replaceVimCharAtCursor('\n')
		}
		return true
	}
	if m.vimInsert {
		if keySpec == "esc" {
			m.vimInsert = false
			m.vimPendingOp = ""
			m.vimPendingObject = ""
			return true
		}
		// Insert mode types normally.
		return false
	}
	// Normal mode: a pending operator (d / y / c) waits for a motion, a text
	// object, or its repeat key (dd / yy / cc).
	if m.vimPendingOp != "" {
		if m.vimPendingObject != "" {
			if m.applyVimOperatorTextObject(keySpec) {
				return true
			}
			// An invalid object key cancels the pending operator.
			m.vimPendingOp = ""
			m.vimPendingObject = ""
		} else if keySpec == m.vimPendingOp {
			m.applyVimLineOperatorRepeat()
			return true
		} else {
			switch {
			case keySpec == "i":
				m.vimPendingObject = "inner"
				return true
			case keySpec == "a":
				m.vimPendingObject = "around"
				return true
			case keySpec == "esc":
				m.vimPendingOp = ""
				return true
			case m.vimOperatorMotion(keySpec):
				return true
			default:
				// A different key cancels the pending operator and dispatches
				// normally below.
				m.vimPendingOp = ""
				m.vimPendingObject = ""
			}
		}
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
	case m.keyMatches("vim_normal", "replace_char", keySpec):
		m.vimPendingReplace = true
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
	case m.keyMatches("vim_normal", "start_change_operator", keySpec):
		m.vimPendingOp = "c"
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
	m.vimSetCursorAtByteOffset(offset)
}

// replaceVimCharAtCursor replaces the grapheme under the cursor with ch,
// staying in normal mode (Rust #39661 replace_char). Newline replacement
// moves the cursor to the start of the next line; any other character leaves
// the cursor on the replacement.
func (m *Model) replaceVimCharAtCursor(ch rune) {
	value := m.composer.Value()
	offset := m.vimValueByteOffset()
	if offset >= len(value) {
		return
	}
	_, size := utf8.DecodeRuneInString(value[offset:])
	replacement := string(ch)
	next := value[:offset] + replacement + value[offset+size:]
	m.composer.SetValue(next)
	if ch == '\n' {
		m.vimSetCursorAtByteOffset(offset + len(replacement))
	} else {
		m.vimSetCursorAtByteOffset(offset)
	}
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
	m.vimSetCursorAtByteOffset(offset)
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
	m.vimSetCursorAtByteOffset(offset)
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
	m.vimSetCursorAtByteOffset(offset + len(m.vimYank))
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

// vimLineStartByteOffset returns the byte offset of the current line's start
// within the composer value.
func (m *Model) vimLineStartByteOffset() int {
	lines := strings.Split(m.composer.Value(), "\n")
	offset := 0
	for i := 0; i < m.composer.Line() && i < len(lines); i++ {
		offset += len(lines[i]) + 1
	}
	return offset
}

// vimSetCursorAtByteOffset positions the textarea cursor at an absolute byte
// offset of the composer value. SetValue leaves the cursor on the last row, so
// the target row is reached by navigating up and the column set within it -
// matching Rust's cursor position after edits (the cursor stays at the edit
// point).
func (m *Model) vimSetCursorAtByteOffset(offset int) {
	value := m.composer.Value()
	if offset < 0 {
		offset = 0
	}
	if offset > len(value) {
		offset = len(value)
	}
	prefix := value[:offset]
	row := strings.Count(prefix, "\n")
	lastRow := m.composer.LineCount() - 1
	for i := 0; i < lastRow-row; i++ {
		m.composer.CursorUp()
	}
	col := offset
	if nl := strings.LastIndex(prefix, "\n"); nl >= 0 {
		col = offset - nl - 1
	}
	m.composer.SetCursor(col)
}

// applyVimLineOperatorRepeat handles dd / yy / cc: the operator repeated on
// itself acts on the whole current line.
func (m *Model) applyVimLineOperatorRepeat() {
	op := m.vimPendingOp
	m.vimPendingOp = ""
	m.vimPendingObject = ""
	switch op {
	case "d":
		m.deleteVimLine()
	case "y":
		m.yankVimLine()
	case "c":
		m.deleteVimLine()
		m.vimInsert = true
	}
}

// vimOperatorMotion applies a pending operator to a horizontal motion
// (h / l / w / b / e / 0 / $). Returns false when keySpec is not an operator
// motion.
func (m *Model) vimOperatorMotion(keySpec string) bool {
	if keySpec == "k" || keySpec == "up" || keySpec == "j" || keySpec == "down" {
		// dj / dk / yj / yk / cj / ck act on the current line and its neighbor
		// (Vim line motions).
		direction := 1
		if keySpec == "k" || keySpec == "up" {
			direction = -1
		}
		m.vimOperatorLineMotion(direction)
		return true
	}
	line := m.vimCurrentLine()
	col := m.vimCursorColumn()
	runes := []rune(line)
	from := runeIndexForByte(line, col)
	var start, end int
	switch {
	case keySpec == "h" || keySpec == "left":
		if col <= 0 {
			m.cancelVimOperator()
			return true
		}
		start, end = col-1, col
	case keySpec == "l" || keySpec == "right":
		if col >= len(line) {
			m.cancelVimOperator()
			return true
		}
		start, end = col, col+1
	case keySpec == "w":
		start, end = col, byteOffsetForRuneIndex(line, vimNextWordStart(runes, from))
	case keySpec == "b":
		start, end = byteOffsetForRuneIndex(line, vimPrevWordStart(runes, from)), col
	case keySpec == "e":
		start, end = col, byteOffsetForRuneIndex(line, vimWordEndIndex(runes, from))+1
	case keySpec == "0":
		start, end = 0, col
	case keySpec == "$" || keySpec == "shift-$":
		start, end = col, len(line)
	default:
		return false
	}
	m.applyVimOperatorRange(start, end)
	return true
}

// applyVimOperatorTextObject applies a pending operator to a text object
// selected after i / a (inner / around) or directly for parens. Returns false
// when keySpec is not a valid text-object key for the current selection mode.
func (m *Model) applyVimOperatorTextObject(keySpec string) bool {
	line := m.vimCurrentLine()
	runes := []rune(line)
	col := m.vimCursorColumn()
	from := runeIndexForByte(line, col)
	var start, end int
	switch {
	case keySpec == "w" || keySpec == "shift-w":
		wordStart := byteOffsetForRuneIndex(line, vimWordStartIndex(runes, from))
		wordEnd := byteOffsetForRuneIndex(line, vimWordEndIndex(runes, from)) + 1
		if m.vimPendingObject == "around" {
			start = vimWordAroundStart(line, wordStart)
			end = vimWordAroundEnd(line, wordEnd)
		} else {
			start, end = wordStart, wordEnd
		}
	case keySpec == "(" || keySpec == "shift-(" || keySpec == ")" || keySpec == "shift-)" || keySpec == "b":
		parenStart, parenEnd, ok := vimEnclosingParens(line, col)
		if !ok {
			m.cancelVimOperator()
			return true
		}
		if m.vimPendingObject == "around" {
			start, end = parenStart, parenEnd+1
		} else {
			start, end = parenStart+1, parenEnd
		}
	default:
		return false
	}
	m.applyVimOperatorRange(start, end)
	return true
}

func (m *Model) cancelVimOperator() {
	m.vimPendingOp = ""
	m.vimPendingObject = ""
}

// vimLineStartOffset returns the byte offset of line row's start in the
// composer value.
func (m *Model) vimLineStartOffset(row int, lines []string) int {
	start := 0
	for i := 0; i < row && i < len(lines); i++ {
		start += len(lines[i]) + 1
	}
	return start
}

// vimLineEndInclusive returns the byte offset just after line row, including
// its trailing newline when present (or the value end for the last line).
func (m *Model) vimLineEndInclusive(row int, lines []string) int {
	end := m.vimLineStartOffset(row, lines) + len(lines[row])
	if row < len(lines)-1 {
		end++
	}
	return end
}

// vimOperatorLineMotion applies the pending operator to the current line plus
// its neighbor: direction 1 (j) adds the next line, -1 (k) adds the previous
// line. At the first/last line the operator acts on the current line only.
func (m *Model) vimOperatorLineMotion(direction int) {
	value := m.composer.Value()
	lines := strings.Split(value, "\n")
	row := m.composer.Line()
	if row < 0 || row >= len(lines) {
		m.cancelVimOperator()
		return
	}
	start := m.vimLineStartOffset(row, lines)
	end := m.vimLineEndInclusive(row, lines)
	if direction > 0 && row+1 < len(lines) {
		end = m.vimLineEndInclusive(row+1, lines)
	} else if direction < 0 && row > 0 {
		start = m.vimLineStartOffset(row-1, lines)
	}
	m.applyVimOperatorValueRange(start, end)
}

// applyVimOperatorRange applies the pending operator (d delete, y yank,
// c change + insert) to the byte range [start, end) of the current line.
func (m *Model) applyVimOperatorRange(start, end int) {
	line := m.vimCurrentLine()
	if start < 0 {
		start = 0
	}
	if end > len(line) {
		end = len(line)
	}
	if start > end {
		start, end = end, start
	}
	m.applyVimOperatorValueRange(m.vimLineStartByteOffset()+start, m.vimLineStartByteOffset()+end)
}

// applyVimOperatorValueRange applies the pending operator to a byte range of
// the full composer value.
func (m *Model) applyVimOperatorValueRange(start, end int) {
	op := m.vimPendingOp
	m.vimPendingOp = ""
	m.vimPendingObject = ""
	value := m.composer.Value()
	if start < 0 {
		start = 0
	}
	if end > len(value) {
		end = len(value)
	}
	if start > end {
		start, end = end, start
	}
	switch op {
	case "d":
		next := value[:start] + value[end:]
		m.composer.SetValue(next)
		m.vimSetCursorAtByteOffset(start)
	case "y":
		m.vimYank = value[start:end]
	case "c":
		next := value[:start] + value[end:]
		m.composer.SetValue(next)
		m.vimSetCursorAtByteOffset(start)
		m.vimInsert = true
	}
}

// vimWordStartIndex returns the rune index of the start of the word under or
// before the cursor.
func vimWordStartIndex(runes []rune, from int) int {
	i := from
	if i >= len(runes) {
		i = len(runes) - 1
	}
	for i > 0 && isVimSpace(runes[i]) {
		i--
	}
	for i > 0 && !isVimSpace(runes[i-1]) {
		i--
	}
	return i
}

// vimWordAroundStart returns the byte offset of the start of the whitespace
// run immediately before the word (or the word start when none).
func vimWordAroundStart(line string, wordStart int) int {
	i := wordStart
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(line[:i])
		if !isVimSpace(r) {
			break
		}
		i -= size
	}
	return i
}

// vimWordAroundEnd returns the byte offset just after the whitespace run
// immediately following the word (or the word end when none).
func vimWordAroundEnd(line string, wordEnd int) int {
	i := wordEnd
	for i < len(line) {
		r, size := utf8.DecodeRuneInString(line[i:])
		if !isVimSpace(r) {
			break
		}
		i += size
	}
	return i
}

// vimEnclosingParens returns the byte range of the innermost enclosing
// parentheses around the cursor column in the current line.
func vimEnclosingParens(line string, col int) (int, int, bool) {
	if col < 0 {
		col = 0
	}
	if col > len(line) {
		col = len(line)
	}
	// Find the nearest unmatched '(' before the cursor, then the matching ')'.
	depth := 0
	open := -1
	for i := 0; i < col; i++ {
		if line[i] == '(' {
			if depth == 0 {
				open = i
			}
			depth++
		} else if line[i] == ')' {
			if depth > 0 {
				depth--
				if depth == 0 {
					open = -1
				}
			}
		}
	}
	if open < 0 {
		return 0, 0, false
	}
	close := -1
	depth = 0
	for i := open; i < len(line); i++ {
		switch line[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				close = i
				i = len(line)
			}
		}
	}
	if close < 0 {
		return 0, 0, false
	}
	return open, close, true
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
