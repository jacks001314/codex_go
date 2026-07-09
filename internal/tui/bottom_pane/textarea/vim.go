package textarea

import "unicode"

// Rust parity subset: codex-rs/tui/src/bottom_pane/textarea/vim.rs.

type VimMode string

const (
	VimInsert VimMode = "insert"
	VimNormal VimMode = "normal"
)

type VimOperator string

const (
	VimOperatorDelete VimOperator = "delete"
	VimOperatorYank   VimOperator = "yank"
	VimOperatorChange VimOperator = "change"
)

type VimPendingKind string

const (
	VimPendingNoneKind       VimPendingKind = "none"
	VimPendingOperatorKind   VimPendingKind = "operator"
	VimPendingTextObjectKind VimPendingKind = "text_object"
)

type VimPending struct {
	Kind     VimPendingKind
	Operator VimOperator
	Scope    VimTextObjectScope
}

type VimMotion string

const (
	VimMotionLeft         VimMotion = "left"
	VimMotionRight        VimMotion = "right"
	VimMotionUp           VimMotion = "up"
	VimMotionDown         VimMotion = "down"
	VimMotionWordForward  VimMotion = "word_forward"
	VimMotionWordBackward VimMotion = "word_backward"
	VimMotionWordEnd      VimMotion = "word_end"
	VimMotionLineStart    VimMotion = "line_start"
	VimMotionLineEnd      VimMotion = "line_end"
)

type VimTextObjectScope string

const (
	VimTextObjectInner  VimTextObjectScope = "inner"
	VimTextObjectAround VimTextObjectScope = "around"
)

type VimTextObject string

const (
	VimTextObjectWord        VimTextObject = "word"
	VimTextObjectBigWord     VimTextObject = "big_word"
	VimTextObjectParentheses VimTextObject = "parentheses"
	VimTextObjectBrackets    VimTextObject = "brackets"
	VimTextObjectBraces      VimTextObject = "braces"
	VimTextObjectDoubleQuote VimTextObject = "double_quote"
	VimTextObjectSingleQuote VimTextObject = "single_quote"
	VimTextObjectBacktick    VimTextObject = "backtick"
)

type TextRange struct {
	Start int
	End   int
}

func (r TextRange) Len() int {
	return r.End - r.Start
}

type TextArea struct {
	Text     string
	Cursor   int
	Elements []TextRange
}

func NewTextArea(text string) *TextArea {
	area := &TextArea{}
	area.SetText(text)
	return area
}

func (a *TextArea) SetText(text string) {
	if a == nil {
		return
	}
	a.Text = text
	a.Cursor = len(text)
}

func (a *TextArea) SetCursor(cursor int) {
	if a == nil {
		return
	}
	a.Cursor = clampByteBoundary(a.Text, cursor)
}

func (a *TextArea) AddElement(start int, end int) {
	if a == nil {
		return
	}
	start = clampByteBoundary(a.Text, start)
	end = clampByteBoundary(a.Text, end)
	if start < end {
		a.Elements = append(a.Elements, TextRange{Start: start, End: end})
	}
}

func (a *TextArea) TextObjectRange(object VimTextObject, scope VimTextObjectScope) (TextRange, bool) {
	if a == nil {
		return TextRange{}, false
	}
	switch object {
	case VimTextObjectWord:
		return a.wordTextObjectRange(scope, false)
	case VimTextObjectBigWord:
		return a.wordTextObjectRange(scope, true)
	case VimTextObjectParentheses:
		return a.pairedTextObjectRange(scope, '(', ')')
	case VimTextObjectBrackets:
		return a.pairedTextObjectRange(scope, '[', ']')
	case VimTextObjectBraces:
		return a.pairedTextObjectRange(scope, '{', '}')
	case VimTextObjectDoubleQuote:
		return a.quotedTextObjectRange(scope, '"')
	case VimTextObjectSingleQuote:
		return a.quotedTextObjectRange(scope, '\'')
	case VimTextObjectBacktick:
		return a.quotedTextObjectRange(scope, '`')
	default:
		return TextRange{}, false
	}
}

func (a *TextArea) wordTextObjectRange(scope VimTextObjectScope, bigWord bool) (TextRange, bool) {
	var inner TextRange
	var ok bool
	if bigWord {
		inner, ok = a.bigWordRangeAtCursor()
	} else {
		inner, ok = a.smallWordRangeAtCursor()
	}
	if !ok {
		return TextRange{}, false
	}
	if scope == VimTextObjectAround {
		return a.expandWordAround(inner), true
	}
	return inner, true
}

func (a *TextArea) bigWordRangeAtCursor() (TextRange, bool) {
	for _, run := range a.nonWhitespaceRuns() {
		if a.cursorOverlapsRange(run) || a.cursorIsAtRangeEnd(run) {
			return run, true
		}
	}
	return TextRange{}, false
}

func (a *TextArea) smallWordRangeAtCursor() (TextRange, bool) {
	for _, run := range a.nonWhitespaceRuns() {
		if !a.cursorOverlapsRange(run) && !a.cursorIsAtRangeEnd(run) {
			continue
		}
		var last TextRange
		hasLast := false
		for _, piece := range splitWordPieces(a.Text[run.Start:run.End]) {
			piece = TextRange{Start: run.Start + piece.Start, End: run.Start + piece.End}
			if a.cursorOverlapsRange(piece) {
				return piece, true
			}
			last = piece
			hasLast = true
		}
		if a.cursorIsAtRangeEnd(run) {
			if hasLast {
				return last, true
			}
			return run, true
		}
		return run, true
	}
	return TextRange{}, false
}

func (a *TextArea) nonWhitespaceRuns() []TextRange {
	runs := []TextRange{}
	start := -1
	for idx, r := range a.Text {
		if unicode.IsSpace(r) {
			if start >= 0 {
				runs = append(runs, TextRange{Start: start, End: idx})
				start = -1
			}
		} else if start < 0 {
			start = idx
		}
	}
	if start >= 0 {
		runs = append(runs, TextRange{Start: start, End: len(a.Text)})
	}
	return runs
}

func (a *TextArea) cursorOverlapsRange(r TextRange) bool {
	return r.Start <= a.Cursor && a.Cursor < r.End
}

func (a *TextArea) cursorIsAtRangeEnd(r TextRange) bool {
	return r.Start < r.End && a.Cursor == r.End
}

func (a *TextArea) expandWordAround(inner TextRange) TextRange {
	following := a.followingWhitespaceEnd(inner.End)
	if following > inner.End {
		return TextRange{Start: inner.Start, End: following}
	}
	return TextRange{Start: a.precedingWhitespaceStart(inner.Start), End: inner.End}
}

func (a *TextArea) followingWhitespaceEnd(start int) int {
	end := start
	for offset, r := range a.Text[start:] {
		if !unicode.IsSpace(r) {
			break
		}
		end = start + offset + len(string(r))
	}
	return end
}

func (a *TextArea) precedingWhitespaceStart(end int) int {
	start := end
	for idx := end; idx > 0; {
		prev := previousRuneStart(a.Text, idx)
		r := []rune(a.Text[prev:idx])[0]
		if !unicode.IsSpace(r) {
			break
		}
		start = prev
		idx = prev
	}
	return start
}

func (a *TextArea) pairedTextObjectRange(scope VimTextObjectScope, open rune, close rune) (TextRange, bool) {
	stack := []int{}
	var best TextRange
	hasBest := false
	for idx, r := range a.Text {
		if a.isInsideElement(idx) {
			continue
		}
		if r == open {
			stack = append(stack, idx)
			continue
		}
		if r != close || len(stack) == 0 {
			continue
		}
		openIdx := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		closeEnd := idx + len(string(r))
		if openIdx <= a.Cursor && a.Cursor <= idx {
			candidate := TextRange{Start: openIdx + len(string(open)), End: idx}
			if scope == VimTextObjectAround {
				candidate = TextRange{Start: openIdx, End: closeEnd}
			}
			if candidate.Start <= candidate.End && (!hasBest || candidate.Len() < best.Len()) {
				best = candidate
				hasBest = true
			}
		}
	}
	return best, hasBest
}

func (a *TextArea) quotedTextObjectRange(scope VimTextObjectScope, quote rune) (TextRange, bool) {
	line := a.currentLineRange()
	open := -1
	var best TextRange
	hasBest := false
	for offset, r := range a.Text[line.Start:line.End] {
		idx := line.Start + offset
		if a.isInsideElement(idx) || r != quote || a.isEscaped(idx) {
			continue
		}
		if open >= 0 {
			if open <= a.Cursor && a.Cursor <= idx {
				candidate := TextRange{Start: open + len(string(quote)), End: idx}
				if scope == VimTextObjectAround {
					candidate = TextRange{Start: open, End: idx + len(string(quote))}
				}
				if candidate.Start <= candidate.End && (!hasBest || candidate.Len() < best.Len()) {
					best = candidate
					hasBest = true
				}
			}
			open = -1
		} else {
			open = idx
		}
	}
	return best, hasBest
}

func (a *TextArea) currentLineRange() TextRange {
	start := 0
	for idx := a.Cursor; idx > 0; {
		prev := previousRuneStart(a.Text, idx)
		if a.Text[prev:idx] == "\n" {
			start = idx
			break
		}
		idx = prev
	}
	end := len(a.Text)
	for idx, r := range a.Text[a.Cursor:] {
		if r == '\n' {
			end = a.Cursor + idx
			break
		}
	}
	return TextRange{Start: start, End: end}
}

func (a *TextArea) isInsideElement(pos int) bool {
	for _, element := range a.Elements {
		if pos >= element.Start && pos < element.End {
			return true
		}
	}
	return false
}

func (a *TextArea) isEscaped(pos int) bool {
	backslashes := 0
	for idx := pos; idx > 0; {
		prev := previousRuneStart(a.Text, idx)
		if a.Text[prev:idx] != "\\" {
			break
		}
		backslashes++
		idx = prev
	}
	return backslashes%2 == 1
}

const wordSeparators = "`~!@#$%^&*()-=+[{]}\\|;:'\",.<>/?"

func splitWordPieces(run string) []TextRange {
	pieces := []TextRange{}
	start := -1
	inSeparator := false
	for idx, r := range run {
		separator := stringsContainsRune(wordSeparators, r)
		if start < 0 {
			start = idx
			inSeparator = separator
			continue
		}
		if separator != inSeparator {
			pieces = append(pieces, TextRange{Start: start, End: idx})
			start = idx
			inSeparator = separator
		}
	}
	if start >= 0 {
		pieces = append(pieces, TextRange{Start: start, End: len(run)})
	}
	return pieces
}

func stringsContainsRune(value string, target rune) bool {
	for _, r := range value {
		if r == target {
			return true
		}
	}
	return false
}

func clampByteBoundary(text string, idx int) int {
	if idx < 0 {
		return 0
	}
	if idx > len(text) {
		return len(text)
	}
	for idx > 0 && idx < len(text) && (text[idx]&0xC0) == 0x80 {
		idx--
	}
	return idx
}

func previousRuneStart(text string, idx int) int {
	if idx > len(text) {
		idx = len(text)
	}
	idx--
	for idx > 0 && (text[idx]&0xC0) == 0x80 {
		idx--
	}
	if idx < 0 {
		return 0
	}
	return idx
}
