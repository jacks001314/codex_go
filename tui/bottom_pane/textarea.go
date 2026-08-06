package bottompane

import (
	"strings"
	"unicode/utf8"

	codextui "codex_go/tui"
	"github.com/rivo/uniseg"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/textarea.rs.

const textAreaWordSeparators = "`~!@#$%^&*()-=+[{]}\\|;:'\",.<>/?"

type TextAreaState struct {
	Text       string
	Cursor     int
	Scroll     int
	KillBuffer string
}

func NewTextAreaState(text string) TextAreaState {
	state := TextAreaState{Text: text}
	state.Cursor = len(text)
	return state
}

func (t *TextAreaState) SetText(text string) {
	if t == nil {
		return
	}
	t.Text = text
	t.Cursor = t.clampToBoundary(t.Cursor)
	t.Scroll = 0
}

func (t *TextAreaState) IsEmpty() bool {
	return t == nil || t.Text == ""
}

func (t *TextAreaState) InsertString(text string) {
	if t == nil || text == "" {
		return
	}
	t.InsertStringAt(t.Cursor, text)
}

func (t *TextAreaState) InsertStringAt(pos int, text string) {
	if t == nil || text == "" {
		return
	}
	pos = t.clampToBoundary(pos)
	t.Text = t.Text[:pos] + text + t.Text[pos:]
	if pos <= t.Cursor {
		t.Cursor += len(text)
	}
	t.Cursor = t.clampToBoundary(t.Cursor)
}

func (t *TextAreaState) ReplaceRange(start int, end int, text string) {
	if t == nil {
		return
	}
	start = t.clampToBoundary(start)
	end = t.clampToBoundary(end)
	if start > end {
		start, end = end, start
	}
	removed := end - start
	t.Text = t.Text[:start] + text + t.Text[end:]
	switch {
	case t.Cursor < start:
	case t.Cursor <= end:
		t.Cursor = start + len(text)
	default:
		t.Cursor += len(text) - removed
	}
	t.Cursor = t.clampToBoundary(t.Cursor)
}

func (t *TextAreaState) SetCursor(pos int) {
	if t == nil {
		return
	}
	t.Cursor = t.clampToBoundary(pos)
}

func (t *TextAreaState) MoveCursorLeft() {
	if t == nil || t.Cursor <= 0 {
		return
	}
	t.Cursor = t.prevBoundary(t.Cursor)
}

func (t *TextAreaState) MoveCursorRight() {
	if t == nil || t.Cursor >= len(t.Text) {
		return
	}
	t.Cursor = t.nextBoundary(t.Cursor)
}

func (t *TextAreaState) MoveCursorToBeginningOfLine() {
	if t == nil {
		return
	}
	t.Cursor = t.BeginningOfCurrentLine()
}

func (t *TextAreaState) MoveCursorToEndOfLine() {
	if t == nil {
		return
	}
	t.Cursor = t.EndOfCurrentLine()
}

func (t *TextAreaState) DeleteBackward(n int) {
	if t == nil || n <= 0 || t.Cursor <= 0 {
		return
	}
	end := t.Cursor
	start := end
	for i := 0; i < n && start > 0; i++ {
		start = t.prevBoundary(start)
	}
	t.ReplaceRange(start, end, "")
}

func (t *TextAreaState) DeleteForward(n int) {
	if t == nil || n <= 0 || t.Cursor >= len(t.Text) {
		return
	}
	start := t.Cursor
	end := start
	for i := 0; i < n && end < len(t.Text); i++ {
		end = t.nextBoundary(end)
	}
	t.ReplaceRange(start, end, "")
}

func (t *TextAreaState) DeleteBackwardWord() {
	if t == nil || t.Cursor <= 0 {
		return
	}
	start := t.BeginningOfPreviousWord()
	t.killRange(start, t.Cursor)
}

func (t *TextAreaState) DeleteForwardWord() {
	if t == nil || t.Cursor >= len(t.Text) {
		return
	}
	end := t.EndOfNextWord()
	if end > t.Cursor {
		t.killRange(t.Cursor, end)
	}
}

func (t *TextAreaState) KillToEndOfLine() {
	if t == nil {
		return
	}
	end := t.EndOfCurrentLine()
	if end == t.Cursor && end < len(t.Text) {
		end = t.nextBoundary(end)
	}
	t.killRange(t.Cursor, end)
}

func (t *TextAreaState) KillToBeginningOfLine() {
	if t == nil {
		return
	}
	start := t.BeginningOfCurrentLine()
	if start == t.Cursor && start > 0 {
		start--
	}
	t.killRange(start, t.Cursor)
}

func (t *TextAreaState) Yank() {
	if t == nil || t.KillBuffer == "" {
		return
	}
	t.InsertString(t.KillBuffer)
}

func (t *TextAreaState) BeginningOfCurrentLine() int {
	if t == nil {
		return 0
	}
	cursor := t.clampToBoundary(t.Cursor)
	if idx := strings.LastIndex(t.Text[:cursor], "\n"); idx >= 0 {
		return idx + 1
	}
	return 0
}

func (t *TextAreaState) EndOfCurrentLine() int {
	if t == nil {
		return 0
	}
	cursor := t.clampToBoundary(t.Cursor)
	if idx := strings.Index(t.Text[cursor:], "\n"); idx >= 0 {
		return cursor + idx
	}
	return len(t.Text)
}

func (t *TextAreaState) BeginningOfPreviousWord() int {
	if t == nil || t.Cursor <= 0 {
		return 0
	}
	cursor := t.clampToBoundary(t.Cursor)
	prefix := t.Text[:cursor]
	runEnd := -1
	for idx := len(prefix); idx > 0; {
		prev := t.prevBoundary(idx)
		r, _ := utf8.DecodeRuneInString(t.Text[prev:idx])
		if !isTextAreaWhitespaceRune(r) {
			runEnd = idx
			break
		}
		idx = prev
	}
	if runEnd < 0 {
		return 0
	}
	runStart := 0
	for idx := t.prevBoundary(runEnd); idx > 0; {
		prev := t.prevBoundary(idx)
		r, _ := utf8.DecodeRuneInString(t.Text[prev:idx])
		if isTextAreaWhitespaceRune(r) {
			runStart = idx
			break
		}
		idx = prev
	}
	pieces := splitTextAreaWordPieces(t.Text[runStart:runEnd])
	if len(pieces) == 0 {
		return runStart
	}
	piece := pieces[len(pieces)-1]
	start := runStart + piece.Start
	if allTextAreaWordSeparators(piece.Text) {
		for i := len(pieces) - 2; i >= 0; i-- {
			if !allTextAreaWordSeparators(pieces[i].Text) {
				break
			}
			start = runStart + pieces[i].Start
		}
	}
	return t.clampToBoundary(start)
}

func (t *TextAreaState) EndOfNextWord() int {
	if t == nil || t.Cursor >= len(t.Text) {
		if t == nil {
			return 0
		}
		return len(t.Text)
	}
	cursor := t.clampToBoundary(t.Cursor)
	suffix := t.Text[cursor:]
	firstNonWS := -1
	for offset, r := range suffix {
		if !isTextAreaWhitespaceRune(r) {
			firstNonWS = offset
			break
		}
	}
	if firstNonWS < 0 {
		return len(t.Text)
	}
	run := suffix[firstNonWS:]
	runEnd := len(run)
	for offset, r := range run {
		if isTextAreaWhitespaceRune(r) {
			runEnd = offset
			break
		}
	}
	pieces := splitTextAreaWordPieces(run[:runEnd])
	if len(pieces) == 0 {
		return cursor + firstNonWS
	}
	piece := pieces[0]
	end := cursor + firstNonWS + piece.Start + len(piece.Text)
	if allTextAreaWordSeparators(piece.Text) {
		for i := 1; i < len(pieces); i++ {
			if !allTextAreaWordSeparators(pieces[i].Text) {
				break
			}
			end = cursor + firstNonWS + pieces[i].Start + len(pieces[i].Text)
		}
	}
	return t.clampToBoundary(end)
}

func (t *TextAreaState) DesiredHeight(width int) int {
	return len(t.WrappedLines(width))
}

func (t *TextAreaState) WrappedLines(width int) []string {
	if t == nil {
		return []string{""}
	}
	return wrappedTextAreaLines(t.Text, width)
}

// wrappedTextAreaLines mirrors Rust textarea wrapped_lines (ad6e48ddd3,
// #37166): overflowing spaces wrap onto their own rows and full logical lines
// reserve a continuation row so the insertion point stays visible.
func wrappedTextAreaLines(text string, width int) []string {
	if width <= 0 {
		width = 1
	}
	lines := []string{}
	for _, logical := range strings.Split(text, "\n") {
		if logical == "" {
			lines = append(lines, "")
			continue
		}
		var line strings.Builder
		used := 0
		graphemes := uniseg.NewGraphemes(logical)
		for graphemes.Next() {
			grapheme := graphemes.Str()
			graphemeWidth := codextui.DisplayWidth(grapheme)
			if used > 0 && used+graphemeWidth > width {
				lines = append(lines, line.String())
				line.Reset()
				used = 0
			}
			line.WriteString(grapheme)
			used += graphemeWidth
		}
		lines = append(lines, line.String())
		if used >= width {
			lines = append(lines, "")
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}

func (t *TextAreaState) CursorPosition(width int, height int) (int, int) {
	if t == nil {
		return 0, 0
	}
	if width <= 0 {
		width = 1
	}
	cursor := t.clampToBoundary(t.Cursor)
	lines := wrappedTextAreaLines(t.Text[:cursor], width)
	row := len(lines) - 1
	col := codextui.DisplayWidth(lines[row])
	// Rust clamps the on-screen column to the last visible cell so a cursor
	// at a full-width boundary never escapes the textarea.
	if col > width-1 {
		col = width - 1
	}
	if height > 0 {
		if row < t.Scroll {
			t.Scroll = row
		}
		if row >= t.Scroll+height {
			t.Scroll = row - height + 1
		}
		if t.Scroll < 0 {
			t.Scroll = 0
		}
		row -= t.Scroll
	}
	return col, row
}

func (t *TextAreaState) HandleKey(key string) {
	if t == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "left":
		t.MoveCursorLeft()
	case "right":
		t.MoveCursorRight()
	case "home", "ctrl+a":
		t.MoveCursorToBeginningOfLine()
	case "end", "ctrl+e":
		t.MoveCursorToEndOfLine()
	case "backspace":
		t.DeleteBackward(1)
	case "delete":
		t.DeleteForward(1)
	case "ctrl+w":
		t.DeleteBackwardWord()
	case "alt+d", "ctrl+delete":
		t.DeleteForwardWord()
	case "ctrl+k":
		t.KillToEndOfLine()
	case "ctrl+u":
		t.KillToBeginningOfLine()
	case "ctrl+y":
		t.Yank()
	case "enter":
		t.InsertString("\n")
	default:
		if len([]rune(key)) == 1 {
			t.InsertString(key)
		}
	}
}

func (t *TextAreaState) clampToBoundary(pos int) int {
	if t == nil {
		return 0
	}
	if pos < 0 {
		return 0
	}
	if pos > len(t.Text) {
		return len(t.Text)
	}
	for pos > 0 && pos < len(t.Text) && !utf8.RuneStart(t.Text[pos]) {
		pos--
	}
	return pos
}

func (t *TextAreaState) prevBoundary(pos int) int {
	pos = t.clampToBoundary(pos)
	if pos <= 0 {
		return 0
	}
	_, size := utf8.DecodeLastRuneInString(t.Text[:pos])
	if size <= 0 {
		return 0
	}
	return pos - size
}

func (t *TextAreaState) nextBoundary(pos int) int {
	pos = t.clampToBoundary(pos)
	if pos >= len(t.Text) {
		return len(t.Text)
	}
	_, size := utf8.DecodeRuneInString(t.Text[pos:])
	if size <= 0 {
		return len(t.Text)
	}
	return pos + size
}

func isTextAreaSpace(text string) bool {
	for _, r := range text {
		return isTextAreaWhitespaceRune(r)
	}
	return false
}

func (t *TextAreaState) killRange(start int, end int) {
	if t == nil {
		return
	}
	start = t.clampToBoundary(start)
	end = t.clampToBoundary(end)
	if start > end {
		start, end = end, start
	}
	if start >= end {
		return
	}
	t.KillBuffer = t.Text[start:end]
	t.ReplaceRange(start, end, "")
}

type textAreaWordPiece struct {
	Start int
	Text  string
}

func splitTextAreaWordPieces(run string) []textAreaWordPiece {
	pieces := []textAreaWordPiece{}
	pieceStart := 0
	var inSeparator bool
	first := true
	for idx, r := range run {
		separator := isTextAreaWordSeparator(r)
		if first {
			inSeparator = separator
			first = false
			continue
		}
		if separator == inSeparator {
			continue
		}
		pieces = append(pieces, textAreaWordPiece{Start: pieceStart, Text: run[pieceStart:idx]})
		pieceStart = idx
		inSeparator = separator
	}
	if !first {
		pieces = append(pieces, textAreaWordPiece{Start: pieceStart, Text: run[pieceStart:]})
	}
	return pieces
}

func isTextAreaWordSeparator(r rune) bool {
	return strings.ContainsRune(textAreaWordSeparators, r)
}

func allTextAreaWordSeparators(text string) bool {
	for _, r := range text {
		if !isTextAreaWordSeparator(r) {
			return false
		}
	}
	return text != ""
}

func isTextAreaWhitespaceRune(r rune) bool {
	return strings.TrimSpace(string(r)) == ""
}
