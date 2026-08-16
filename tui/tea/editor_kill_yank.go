package tea

import (
	bubbletea "github.com/charmbracelet/bubbletea"
)

// applyEditorKillYankKey dispatches the composer editor kill/yank actions that
// the bubbletea textarea does not handle natively, mirroring Rust
// bottom_pane/textarea.rs: kill_line_start (ctrl-u) and kill_line_end (ctrl-k)
// cut the text between the cursor and the line boundary into a single-entry
// kill buffer (at the boundary the adjacent newline is killed instead, like
// Rust), kill_whole_line cuts the current line, and yank (ctrl-y) pastes the
// kill buffer at the cursor. The buffer survives composer-level clears
// (submit / slash dispatch), matching Rust's single-entry kill buffer.
func (m *Model) applyEditorKillYankKey(msg bubbletea.KeyMsg, keySpec string) bool {
	if m == nil || m.modal != nil || m.overlay != nil || m.slashPopup.Active || m.skillPopup.Active {
		return false
	}
	switch {
	case m.keyMatches("editor", "kill_line_start", keySpec):
		m.killComposerToLineStart()
	case m.keyMatches("editor", "kill_line_end", keySpec):
		m.killComposerToLineEnd()
	case m.keyMatches("editor", "kill_whole_line", keySpec):
		m.killComposerWholeLine()
	case m.keyMatches("editor", "yank", keySpec):
		m.yankComposerKillBuffer()
	default:
		return false
	}
	return true
}

// killComposerToLineEnd cuts from the cursor to the end of the current line
// into the kill buffer; at the line end the trailing newline is cut instead.
func (m *Model) killComposerToLineEnd() {
	line := m.vimCurrentLine()
	col := m.vimCursorColumn()
	lineStart := m.vimLineStartByteOffset()
	eol := lineStart + len(line)
	if col >= len(line) {
		// Cursor at the line end: kill the newline when present.
		if eol < len(m.composer.Value()) {
			m.editorKillRange(eol, eol+1)
		}
		return
	}
	m.editorKillRange(lineStart+col, eol)
}

// killComposerToLineStart cuts from the start of the current line to the
// cursor into the kill buffer; at the line start the preceding newline is cut
// instead.
func (m *Model) killComposerToLineStart() {
	col := m.vimCursorColumn()
	lineStart := m.vimLineStartByteOffset()
	if col == 0 {
		if lineStart > 0 {
			m.editorKillRange(lineStart-1, lineStart)
		}
		return
	}
	m.editorKillRange(lineStart, lineStart+col)
}

// killComposerWholeLine cuts the whole current line (including its newline)
// into the kill buffer.
func (m *Model) killComposerWholeLine() {
	line := m.vimCurrentLine()
	lineStart := m.vimLineStartByteOffset()
	end := lineStart + len(line)
	if end < len(m.composer.Value()) {
		end++
	}
	m.editorKillRange(lineStart, end)
}

// yankComposerKillBuffer pastes the kill buffer at the cursor.
func (m *Model) yankComposerKillBuffer() {
	if m.composerKillBuffer == "" {
		return
	}
	offset := m.vimValueByteOffset()
	value := m.composer.Value()
	m.composer.SetValue(value[:offset] + m.composerKillBuffer + value[offset:])
	m.vimSetCursorAtByteOffset(offset + len(m.composerKillBuffer))
}

// editorKillRange removes [start, end) from the composer value, storing it in
// the single-entry kill buffer (byte offsets; cursor columns are byte-based
// like SetCursor).
func (m *Model) editorKillRange(start, end int) {
	value := m.composer.Value()
	if start < 0 {
		start = 0
	}
	if end > len(value) {
		end = len(value)
	}
	if start >= end {
		return
	}
	m.composerKillBuffer = value[start:end]
	m.composer.SetValue(value[:start] + value[end:])
	m.vimSetCursorAtByteOffset(start)
}
