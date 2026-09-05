package tea

import (
	"strings"
	"unicode"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// vimSearchRange is a byte-offset match range in the composer draft.
type vimSearchRange struct {
	Start int
	End   int
}

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
	// Rust #41586: while a Vim search query is being entered, typed runes
	// accumulate into the query, Enter performs the search and Esc cancels it.
	if m.vimSearchMode {
		if keySpec == "esc" {
			m.cancelVimSearch()
			return true
		}
		if keySpec == "enter" {
			m.executeVimSearch()
			return true
		}
		if msg.Type == bubbletea.KeyRunes && len(msg.Runes) == 1 {
			m.vimSearchQuery += string(msg.Runes[0])
		}
		return true
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
	// A pending find/till motion (f/F/t/T) consumes the next typed character as
	// the search target, mirroring Rust vim_commands.rs VimPending::Find.
	if m.vimPendingFind {
		if msg.Type == bubbletea.KeyRunes && len(msg.Runes) == 1 {
			m.resolveVimFind(msg.Runes[0])
		} else if keySpec == "esc" {
			m.clearVimFind()
			m.cancelVimOperator()
		}
		return true
	}
	// The `gg` buffer-top jump chord (Rust #40958 vim_commands.rs jump_top):
	// a pending `g` is completed by a second `g`; any other key cancels the
	// chord and lets the current key be dispatched normally below.
	if m.vimPendingG {
		m.vimPendingG = false
		if keySpec == "g" {
			m.jumpToVimBufferLine(false)
			return true
		}
	}
	// Vim replace mode (R, Rust #42194): typed characters overwrite the draft
	// under the cursor and advance; Esc returns to normal mode.
	if m.vimReplaceMode {
		switch {
		case keySpec == "esc" || keySpec == "escape":
			m.vimReplaceMode = false
		case msg.Type == bubbletea.KeyRunes && len(msg.Runes) == 1:
			m.replaceVimCharAdvancing(msg.Runes[0])
		case keySpec == "enter":
			m.replaceVimCharAdvancing('\n')
		case keySpec == "backspace" || keySpec == "left":
			m.vimCursorLeft()
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
			case keySpec == "g":
				// dg / yg / cg begin the `gg` buffer-top chord with the
				// pending operator attached (Rust operator motion_jump_top).
				m.vimPendingG = true
				return true
			case keySpec == "shift-g":
				// dG / yG / cG apply the operator over the buffer-bottom
				// range (Rust operator motion_jump_bottom).
				m.jumpToVimBufferLine(true)
				return true
			case m.keyMatches("vim_normal", "search_forward", keySpec):
				// d/ y/ c/ start a search with the pending operator attached
				// (Rust #41586 vim_search.rs operator transaction).
				m.startVimSearch(true, m.vimPendingOp)
				return true
			case m.keyMatches("vim_normal", "search_backward", keySpec):
				m.startVimSearch(false, m.vimPendingOp)
				return true
			case m.keyMatches("vim_normal", "search_next", keySpec):
				// d n / y n / c n apply the operator to the next match of the
				// last query (Rust SearchCommand::Next with VimPending::Operator).
				m.jumpVimSearchMatch(true)
				return true
			case m.keyMatches("vim_normal", "search_prev", keySpec):
				m.jumpVimSearchMatch(false)
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
	// Buffer-jump motions in normal mode (Rust #40958): `gg` jumps to the
	// first buffer line (a two-key chord started by the pending-g state), `G`
	// jumps to the last buffer line.
	if keySpec == "g" {
		m.vimPendingG = true
		return true
	}
	if keySpec == "shift-g" {
		m.jumpToVimBufferLine(true)
		return true
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
	case m.keyMatches("vim_normal", "repeat_last_change", keySpec):
		m.applyVimDotRepeat()
	case m.keyMatches("vim_normal", "replace_char", keySpec):
		m.vimPendingReplace = true
	case m.keyMatches("vim_normal", "replace_mode", keySpec):
		m.vimReplaceMode = true
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
	case m.keyMatches("vim_normal", "search_forward", keySpec):
		m.startVimSearch(true, "")
	case m.keyMatches("vim_normal", "search_backward", keySpec):
		m.startVimSearch(false, "")
	case m.keyMatches("vim_normal", "search_next", keySpec):
		m.jumpVimSearchMatch(true)
	case m.keyMatches("vim_normal", "search_prev", keySpec):
		m.jumpVimSearchMatch(false)
	case m.keyMatches("vim_normal", "find_char_forward", keySpec):
		m.startVimFind(0, true, "")
	case m.keyMatches("vim_normal", "find_char_backward", keySpec):
		m.startVimFind(0, false, "")
	case m.keyMatches("vim_normal", "till_char_forward", keySpec):
		m.startVimFind(1, true, "")
	case m.keyMatches("vim_normal", "till_char_backward", keySpec):
		m.startVimFind(1, false, "")
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
	m.recordVimDelete(value[offset : offset+size])
	m.composer.SetValue(value[:offset] + value[offset+size:])
	m.vimSetCursorAtByteOffset(offset)
}

// recordVimDelete records the last completed Vim delete for dot-repeat (`.`),
// mirroring Rust VimCommandState::last_change (#40521). A deletion spanning
// multiple runes or containing whitespace is treated as a word delete.
func (m *Model) recordVimDelete(deleted string) {
	m.vimHasLastDelete = true
	m.vimLastDeleteWord = utf8.RuneCountInString(deleted) > 1 || strings.ContainsAny(deleted, " \t")
}

// applyVimDotRepeat replays the last completed Vim delete (`.`), mirroring Rust
// vim_normal.repeat_last_change (#40521). When no complete edit is recorded the
// key is a no-op.
func (m *Model) applyVimDotRepeat() bool {
	if m == nil || !m.vimHasLastDelete {
		return false
	}
	if m.vimLastDeleteWord {
		m.deleteVimWordAtCursor()
	} else {
		m.deleteVimCharAtCursor()
	}
	return true
}

// deleteVimWordAtCursor deletes the word under/after the cursor (the `dw` word
// motion), used to replay a recorded word delete.
func (m *Model) deleteVimWordAtCursor() {
	line := m.vimCurrentLine()
	col := m.vimCursorColumn()
	runes := []rune(line)
	from := runeIndexForByte(line, col)
	start := col
	end := byteOffsetForRuneIndex(line, vimNextWordStart(runes, from))
	value := m.composer.Value()
	startByte := m.vimLineStartByteOffset() + start
	endByte := m.vimLineStartByteOffset() + end
	if startByte < 0 || endByte > len(value) || startByte >= endByte {
		return
	}
	m.composer.SetValue(value[:startByte] + value[endByte:])
	m.vimSetCursorAtByteOffset(startByte)
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

// replaceVimCharAdvancing replaces the rune under the cursor and moves to the
// next character, or appends at the end of the draft (Vim replace mode, Rust
// #42194). A newline replacement moves the cursor into the following line.
func (m *Model) replaceVimCharAdvancing(ch rune) {
	if m == nil {
		return
	}
	value := m.composer.Value()
	offset := m.vimValueByteOffset()
	replacement := string(ch)
	if offset >= len(value) {
		m.composer.SetValue(value + replacement)
		m.vimSetCursorAtByteOffset(len(value) + len(replacement))
		return
	}
	_, size := utf8.DecodeRuneInString(value[offset:])
	next := value[:offset] + replacement + value[offset+size:]
	m.composer.SetValue(next)
	m.vimSetCursorAtByteOffset(offset + len(replacement))
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

// startVimFind begins a find/till motion that waits for the next typed
// character as its target. kind 0 = find (land on the target), 1 = till (stop
// just before/after); forward selects f/t vs F/T; operator carries a pending
// d/y/c when f/t is used as an operator motion.
func (m *Model) startVimFind(kind int, forward bool, operator string) {
	m.vimPendingFind = true
	m.vimFindKind = kind
	m.vimFindForward = forward
	m.vimFindOperator = operator
}

func (m *Model) clearVimFind() {
	m.vimPendingFind = false
	m.vimFindKind = 0
	m.vimFindForward = false
	m.vimFindOperator = ""
}

// startVimSearch begins a Vim composer search (Rust #41586 vim_search.rs):
// typed runes become the query until Enter executes it or Esc cancels it.
// When operator is non-empty (d / y / c pressed before the search), the
// accepted search applies that operator over the cursor->match range instead
// of being a plain motion.
func (m *Model) startVimSearch(forward bool, operator string) {
	m.vimSearchMode = true
	m.vimSearchForward = forward
	m.vimSearchQuery = ""
	m.vimSearchMatches = nil
	m.vimSearchIndex = -1
	m.vimSearchOp = operator
	m.vimPendingOp = ""
	m.vimPendingObject = ""
}

// cancelVimSearch abandons the active search query and clears its state.
func (m *Model) cancelVimSearch() {
	m.vimSearchMode = false
	m.vimSearchQuery = ""
	m.vimSearchMatches = nil
	m.vimSearchIndex = -1
	m.vimSearchOp = ""
}

// renderVimSearchFooter returns the live Vim search query footer (Rust #41586
// bottom_pane/textarea/vim_search.rs SearchInput): the direction sigil followed
// by the query being entered.
func (m *Model) renderVimSearchFooter() string {
	prefix := "/"
	if !m.vimSearchForward {
		prefix = "?"
	}
	return m.footerStyle.Render(prefix + m.vimSearchQuery)
}

// executeVimSearch finds matches for the entered query and lands on the first
// match relative to the cursor. When a pending d/y/c operator was carried into
// the search, it applies that operator over the cursor->match range instead.
func (m *Model) executeVimSearch() {
	query := m.vimSearchQuery
	forward := m.vimSearchForward
	operator := m.vimSearchOp
	m.vimSearchMode = false
	m.vimSearchOp = ""
	m.vimSearchQuery = query
	if query == "" {
		m.vimSearchMatches = nil
		m.vimSearchIndex = -1
		return
	}
	m.vimSearchMatches = findVimSearchMatches(m.composer.Value(), query)
	if len(m.vimSearchMatches) == 0 {
		m.vimSearchIndex = -1
		return
	}
	m.vimSearchIndex = m.firstVimSearchIndexFrom(m.vimValueByteOffset(), forward)
	if operator != "" {
		m.applyVimSearchOperator(operator, m.vimSearchMatches[m.vimSearchIndex].Start)
		return
	}
	m.applyVimSearchMatch()
}

// jumpVimSearchMatch moves to the next (or previous) search match, wrapping.
func (m *Model) jumpVimSearchMatch(next bool) {
	operator := m.vimSearchOp
	if operator == "" {
		operator = m.vimPendingOp
	}
	if len(m.vimSearchMatches) == 0 {
		if m.vimSearchQuery == "" {
			return
		}
		m.vimSearchMatches = findVimSearchMatches(m.composer.Value(), m.vimSearchQuery)
		m.vimSearchIndex = m.firstVimSearchIndexFrom(m.vimValueByteOffset(), m.vimSearchForward)
		if len(m.vimSearchMatches) == 0 {
			return
		}
	}
	if next {
		m.vimSearchIndex++
		if m.vimSearchIndex >= len(m.vimSearchMatches) {
			m.vimSearchIndex = 0
		}
	} else {
		m.vimSearchIndex--
		if m.vimSearchIndex < 0 {
			m.vimSearchIndex = len(m.vimSearchMatches) - 1
		}
	}
	if operator != "" {
		m.vimSearchOp = ""
		m.vimPendingOp = ""
		m.applyVimSearchOperator(operator, m.vimSearchMatches[m.vimSearchIndex].Start)
		return
	}
	m.applyVimSearchMatch()
}

// applyVimSearchOperator applies a pending d/y/c operator to the byte range
// spanning the cursor and the target search match (Rust #41586
// bottom_pane/textarea/vim_search.rs apply_vim_search operator transaction).
func (m *Model) applyVimSearchOperator(operator string, target int) {
	origin := m.vimValueByteOffset()
	if origin == target {
		return
	}
	start, end := origin, target
	if start > end {
		start, end = end, start
	}
	value := m.composer.Value()
	valueLen := len(value)
	if start < 0 {
		start = 0
	}
	if end > valueLen {
		end = valueLen
	}
	// A motion end at column zero may be treated as linewise when the spanned
	// text up to the line start is blank; an exclusive line-start end is
	// otherwise retracted by one byte.
	if start <= valueLen {
		lineStart := vimLineStartByteOffsetFor(value, start)
		if lineStart > start {
			lineStart = start
		}
		_, colEnd := vimLineColumnForByteOffset(value, end)
		endsAtLineStart := colEnd == 0 && end <= valueLen
		linewise := endsAtLineStart && strings.TrimSpace(value[lineStart:start]) == ""
		if linewise {
			start = lineStart
		} else if endsAtLineStart && end > 0 {
			end--
		}
	}
	m.vimPendingOp = operator
	if operator == "y" {
		m.vimSearchMoveCursor(start)
	}
	m.applyVimOperatorValueRange(start, end)
}

// applyVimSearchMatch moves the composer cursor to the current search match.
func (m *Model) applyVimSearchMatch() {
	if m.vimSearchIndex < 0 || m.vimSearchIndex >= len(m.vimSearchMatches) {
		return
	}
	m.vimSearchMoveCursor(m.vimSearchMatches[m.vimSearchIndex].Start)
}

// vimSearchMoveCursor positions the composer cursor at a byte offset within the
// value, moving to the containing logical line first.
func (m *Model) vimSearchMoveCursor(offset int) {
	row, col := vimLineColumnForByteOffset(m.composer.Value(), offset)
	for m.composer.Line() < row {
		m.composer.CursorDown()
	}
	for m.composer.Line() > row {
		m.composer.CursorUp()
	}
	m.composer.SetCursor(col)
}

func vimLineColumnForByteOffset(value string, offset int) (row int, col int) {
	lines := strings.Split(value, "\n")
	remaining := offset
	for i, line := range lines {
		if remaining <= len(line) {
			return i, remaining
		}
		remaining -= len(line) + 1
	}
	if len(lines) == 0 {
		return 0, 0
	}
	return len(lines) - 1, len(lines[len(lines)-1])
}

// vimLineStartByteOffsetFor returns the byte offset of the start of the logical
// line containing offset within value.
func vimLineStartByteOffsetFor(value string, offset int) int {
	if offset <= 0 {
		return 0
	}
	lines := strings.Split(value, "\n")
	remaining := offset
	start := 0
	for i, line := range lines {
		if remaining <= len(line) {
			return start
		}
		remaining -= len(line) + 1
		start += len(line) + 1
		if i == len(lines)-1 {
			return start
		}
	}
	return start
}

// firstVimSearchIndexFrom returns the match index nearest the cursor in the
// given direction, wrapping to the other end when no match lies beyond it.
func (m *Model) firstVimSearchIndexFrom(cursor int, forward bool) int {
	if len(m.vimSearchMatches) == 0 {
		return -1
	}
	if forward {
		for i, match := range m.vimSearchMatches {
			if match.Start >= cursor {
				return i
			}
		}
		return 0
	}
	for i := len(m.vimSearchMatches) - 1; i >= 0; i-- {
		if m.vimSearchMatches[i].Start < cursor {
			return i
		}
	}
	return len(m.vimSearchMatches) - 1
}

// findVimSearchMatches returns the byte-offset ranges of case-insensitive
// substring matches of query within text, mapping folded (lowercased) byte
// ranges back to original Unicode byte ranges.
func findVimSearchMatches(text, query string) []vimSearchRange {
	if text == "" || query == "" {
		return nil
	}
	queryLower := strings.ToLower(query)
	if queryLower == "" {
		return nil
	}
	type foldedSpan struct {
		foldedStart  int
		foldedEnd    int
		originalFrom int
		originalTo   int
	}
	folded := strings.Builder{}
	spans := []foldedSpan{}
	for originalFrom, r := range text {
		originalTo := originalFrom + utf8.RuneLen(r)
		lower := unicode.ToLower(r)
		foldedStart := folded.Len()
		folded.WriteRune(lower)
		spans = append(spans, foldedSpan{foldedStart: foldedStart, foldedEnd: folded.Len(), originalFrom: originalFrom, originalTo: originalTo})
	}
	foldedText := folded.String()
	ranges := []vimSearchRange{}
	searchFrom := 0
	for searchFrom <= len(foldedText) {
		relativeStart := strings.Index(foldedText[searchFrom:], queryLower)
		if relativeStart < 0 {
			break
		}
		foldedStart := searchFrom + relativeStart
		foldedEnd := foldedStart + len(queryLower)
		originalFrom := -1
		originalTo := 0
		for _, span := range spans {
			if span.foldedEnd <= foldedStart || span.foldedStart >= foldedEnd {
				continue
			}
			if originalFrom < 0 {
				originalFrom = span.originalFrom
			}
			originalTo = span.originalTo
		}
		if originalFrom >= 0 {
			ranges = append(ranges, vimSearchRange{Start: originalFrom, End: originalTo})
		}
		searchFrom = foldedEnd
	}
	return ranges
}

// resolveVimFind applies a find/till motion to the current line using the
// captured kind/direction/operator. When a pending operator is present the
// motion applies it over the resulting character range; otherwise the cursor
// moves to the destination (mirrors Rust find_vim_character).
func (m *Model) resolveVimFind(target rune) {
	kind := m.vimFindKind
	forward := m.vimFindForward
	operator := m.vimFindOperator
	m.clearVimFind()

	line := m.vimCurrentLine()
	col := m.vimCursorColumn()
	from := runeIndexForByte(line, col)
	idx, ok := vimFindCharIndex(line, from, target, kind, forward)
	if !ok {
		if operator != "" {
			m.cancelVimOperator()
		}
		return
	}
	if operator == "" {
		m.composer.SetCursor(byteOffsetForRuneIndex(line, idx))
		return
	}
	bytePos := byteOffsetForRuneIndex(line, idx)
	targetLen := len(string([]rune(line)[idx]))
	var start, end int
	switch {
	case forward && kind == 0: // f: cursor..target end
		start, end = col, bytePos+targetLen
	case !forward && kind == 0: // F: target start..cursor
		start, end = bytePos, col
	case forward && kind == 1: // t: cursor..target start
		start, end = col, bytePos
	default: // T: target end..cursor
		start, end = bytePos+targetLen, col
	}
	m.applyVimOperatorRange(start, end)
}

// vimFindCharIndex returns the rune index of the destination for a find/till
// motion on the current line. kind 0 = find (land on target), 1 = till (stop
// just before/after the target). The search is line-local.
func vimFindCharIndex(line string, from int, target rune, kind int, forward bool) (int, bool) {
	runes := []rune(line)
	if forward {
		for i := from + 1; i < len(runes); i++ {
			if runes[i] != target {
				continue
			}
			if kind == 0 {
				return i, true
			}
			if i-1 >= from {
				return i - 1, true
			}
			return from, true
		}
		return 0, false
	}
	for i := from - 1; i >= 0; i-- {
		if runes[i] != target {
			continue
		}
		if kind == 0 {
			return i, true
		}
		return i + 1, true
	}
	return 0, false
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
	// Character find/till operator motions (d f / y t etc.): f/F/t/T capture a
	// pending find with the current operator, mirroring Rust VimPending::Find.
	if keySpec == "f" || keySpec == "shift-f" || keySpec == "F" || keySpec == "t" || keySpec == "shift-t" || keySpec == "T" {
		kind, forward := 0, true
		switch {
		case keySpec == "shift-f" || keySpec == "F":
			kind, forward = 0, false
		case keySpec == "t":
			kind, forward = 1, true
		case keySpec == "shift-t" || keySpec == "T":
			kind, forward = 1, false
		}
		m.startVimFind(kind, forward, m.vimPendingOp)
		return true
	}
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
		m.recordVimDelete(value[start:end])
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

// jumpToVimBufferLine mirrors Rust vim_commands.rs jump_to_vim_buffer_line
// (#40958): with a pending operator it applies the operator over the buffer
// jump range (gg deletes/yanks/changes the start of the buffer through the end
// of the current line; G covers the current line through the end of the
// buffer). Without an operator it moves the cursor to the first non-blank of
// the first (gg) or last (G) buffer line.
func (m *Model) jumpToVimBufferLine(last bool) {
	op := m.vimPendingOp
	if op != "" {
		value := m.composer.Value()
		lines := strings.Split(value, "\n")
		row := m.composer.Line()
		if row < 0 || row >= len(lines) {
			m.cancelVimOperator()
			return
		}
		var start, end int
		if last {
			start, end = m.vimLineStartOffset(row, lines), len(value)
		} else {
			start, end = 0, m.vimLineEndInclusive(row, lines)
		}
		if start > end {
			start, end = end, start
		}
		m.applyVimOperatorValueRange(start, end)
		return
	}

	// Plain motion: move to the target line and land on its first non-blank.
	if last {
		for m.composer.Line() < m.composer.LineCount()-1 {
			m.composer.CursorDown()
		}
		m.composer.SetCursor(0)
	} else {
		for m.composer.Line() > 0 {
			m.composer.CursorUp()
		}
		m.composer.SetCursor(0)
	}
	m.vimCursorLineStartNonBlank()
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
