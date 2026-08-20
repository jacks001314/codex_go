package bottompane

import (
	"strings"
	"time"
	"unicode"
)

// Rust parity: codex-rs/tui/src/bottom_pane/custom_prompt_view.rs.

// NormalizePastedText mirrors Rust #38704: pasted text may contain CRLF pairs
// or bare CRs (e.g., iTerm2), but the composer expects LF. Normalize CRLF
// pairs before bare CRs so each pasted line break becomes one LF and existing
// LFs stay unchanged.
func NormalizePastedText(pasted string) string {
	return strings.ReplaceAll(strings.ReplaceAll(pasted, "\r\n", "\n"), "\r", "\n")
}

type CustomPromptCompletion string

const (
	CustomPromptPending   CustomPromptCompletion = ""
	CustomPromptAccepted  CustomPromptCompletion = "accepted"
	CustomPromptCancelled CustomPromptCompletion = "cancelled"
)

type CustomPromptView struct {
	Title        string
	Placeholder  string
	Text         string
	ContextLabel string

	Submitted  []string
	Completion CustomPromptCompletion

	pasteBurst  PasteBurst
	cursor      int
	vimEnabled  bool
	vimInsert   bool
	vimPendingOp string
}

func NewCustomPromptView(title string, placeholder string, initialText string, contextLabel string) *CustomPromptView {
	view := &CustomPromptView{
		Title:        title,
		Placeholder:  placeholder,
		Text:         initialText,
		ContextLabel: contextLabel,
		cursor:       len([]rune(initialText)),
		vimInsert:    true,
	}
	return view
}

// SetVimEnabled applies the composer's Vim preference to the prompt, starting
// in insert mode so the prompt is immediately ready for input (Rust #39618
// enable_vim_in_insert_mode).
func (v *CustomPromptView) SetVimEnabled(enabled bool) {
	if v == nil {
		return
	}
	v.vimEnabled = enabled
	v.vimInsert = true
	v.vimPendingOp = ""
}

func (v *CustomPromptView) VimEnabled() bool {
	return v != nil && v.vimEnabled
}

func (v *CustomPromptView) VimInsert() bool {
	return v != nil && (!v.vimEnabled || v.vimInsert)
}

func (v *CustomPromptView) HandleKey(key string) {
	v.HandleKeyAt(key, time.Now())
}

func (v *CustomPromptView) HandleKeyAt(key string, now time.Time) {
	if v == nil || v.IsComplete() {
		return
	}
	rawKey := key
	key = normalizeCustomPromptKey(key)
	if v.vimEnabled && !v.vimInsert {
		if v.handleVimNormalKey(rawKey, key) {
			return
		}
	}
	switch key {
	case "esc", "escape", "ctrl-c":
		if v.vimEnabled && !v.vimInsert {
			v.Completion = CustomPromptCancelled
		} else if v.vimEnabled && v.vimInsert {
			// First Esc in a Vim prompt enters normal mode (Rust #39618).
			v.vimInsert = false
			v.vimPendingOp = ""
		} else {
			v.Completion = CustomPromptCancelled
		}
	case "enter":
		if v.pasteBurst.DirectInsertNewlineShouldInsert(now) {
			v.pasteBurst.ExtendWindow(now)
			v.InsertString("\n")
			return
		}
		if v.vimEnabled && v.vimInsert {
			v.Submit()
			return
		}
		v.Submit()
	case "backspace":
		v.Backspace()
	case "left", "ctrl-b":
		v.MoveCursor(-1)
	case "right", "ctrl-f":
		v.MoveCursor(1)
	case "up", "ctrl-p":
		v.MoveLine(-1)
	case "down", "ctrl-n":
		v.MoveLine(1)
	case "home", "ctrl-a":
		v.MoveLineStart()
	case "end", "ctrl-e":
		v.MoveLineEnd()
	case "alt-b", "alt-left", "ctrl-left":
		v.MoveWord(-1)
	case "alt-f", "alt-right", "ctrl-right":
		v.MoveWord(1)
	case "ctrl-w", "alt-backspace", "ctrl-backspace":
		v.DeleteWordBackward()
	case "alt-d", "ctrl-delete", "alt-delete":
		v.DeleteWordForward()
	case "ctrl-u":
		v.KillLineStart()
	case "ctrl-k":
		v.KillLineEnd()
	case "tab":
		return
	default:
		if strings.HasPrefix(key, "paste:") {
			v.HandlePaste(strings.TrimPrefix(key, "paste:"))
		}
	}
}

// handleVimNormalKey dispatches Vim normal-mode keys: i/a/A/I/O/o enter
// insert, h/l/k/j/w/b/e/0/$ move, x/D delete, s/C change, r waits for the
// replacement character, and Esc cancels a pending operator or exits the
// prompt (Rust #39618 / #39661).
func (v *CustomPromptView) handleVimNormalKey(rawKey string, key string) bool {
	if v == nil {
		return false
	}
	if v.vimPendingOp == "r" {
		v.vimPendingOp = ""
		if key == "esc" || key == "escape" {
			return true
		}
		if len(rawKey) == 1 {
			v.ReplaceCharAtCursor([]rune(rawKey)[0])
		} else if key == "enter" {
			v.ReplaceCharAtCursor('\n')
		}
		return true
	}
	switch key {
	case "i", "insert":
		v.vimInsert = true
	case "a":
		v.MoveCursor(1)
		v.vimInsert = true
	case "shift-a", "a+shift":
		v.MoveLineEnd()
		v.vimInsert = true
	case "shift-i", "i+shift":
		v.MoveLineStart()
		v.vimInsert = true
	case "o":
		v.MoveLineEnd()
		v.InsertString("\n")
		v.vimInsert = true
	case "shift-o", "o+shift":
		v.MoveLineStart()
		v.InsertString("\n")
		v.MoveCursor(-1)
		v.vimInsert = true
	case "h", "left":
		v.MoveCursor(-1)
	case "l", "right":
		v.MoveCursor(1)
	case "k", "up":
		v.MoveLine(-1)
	case "j", "down":
		v.MoveLine(1)
	case "w":
		v.MoveWord(1)
	case "b":
		v.MoveWord(-1)
	case "e":
		v.MoveWordEnd()
	case "0":
		v.MoveLineStart()
	case "$", "shift-$":
		v.MoveLineEnd()
	case "x":
		v.DeleteCharAtCursor()
	case "s":
		v.DeleteCharAtCursor()
		v.vimInsert = true
	case "d", "shift-d", "D":
		if key == "d" {
			v.DeleteLine()
		} else {
			v.KillLineEnd()
		}
	case "shift-c", "C":
		v.KillLineEnd()
		v.vimInsert = true
	case "r":
		v.vimPendingOp = "r"
	case "esc", "escape":
		if v.vimPendingOp != "" {
			v.vimPendingOp = ""
			return true
		}
		v.Completion = CustomPromptCancelled
	default:
		return false
	}
	return true
}

func (v *CustomPromptView) HandleRuneAt(ch rune, now time.Time) {
	if v == nil || v.IsComplete() {
		return
	}
	_, pasteLikeBurst := v.pasteBurst.OnPlainCharNoHold(now)
	v.InsertString(string(ch))
	if pasteLikeBurst {
		v.pasteBurst.ExtendWindow(now)
	}
}

func (v *CustomPromptView) HandleTextAt(text string, start time.Time, step time.Duration) {
	now := start
	for _, ch := range text {
		v.HandleRuneAt(ch, now)
		now = now.Add(step)
	}
}

func (v *CustomPromptView) HandlePaste(pasted string) bool {
	if v == nil || pasted == "" || v.IsComplete() {
		return false
	}
	// Rust #38704: CRLF pairs and bare CRs normalize to LF so each pasted
	// line break becomes a single newline.
	pasted = NormalizePastedText(pasted)
	v.InsertString(pasted)
	v.pasteBurst.ClearAfterExplicitPaste()
	return true
}

func (v *CustomPromptView) Submit() bool {
	if v == nil || v.IsComplete() {
		return false
	}
	text := strings.TrimSpace(v.Text)
	if text == "" {
		return false
	}
	v.Submitted = append(v.Submitted, text)
	v.Completion = CustomPromptAccepted
	return true
}

func (v *CustomPromptView) Cancel() {
	if v == nil || v.IsComplete() {
		return
	}
	v.Completion = CustomPromptCancelled
}

func (v *CustomPromptView) IsComplete() bool {
	return v != nil && v.Completion != CustomPromptPending
}

func (v *CustomPromptView) LastSubmitted() (string, bool) {
	if v == nil || len(v.Submitted) == 0 {
		return "", false
	}
	return v.Submitted[len(v.Submitted)-1], true
}

func (v *CustomPromptView) InsertString(text string) {
	if v == nil || text == "" {
		return
	}
	runes := []rune(v.Text)
	if v.cursor < 0 || v.cursor > len(runes) {
		v.cursor = len(runes)
	}
	insert := []rune(text)
	next := make([]rune, 0, len(runes)+len(insert))
	next = append(next, runes[:v.cursor]...)
	next = append(next, insert...)
	next = append(next, runes[v.cursor:]...)
	v.Text = string(next)
	v.cursor += len(insert)
}

func (v *CustomPromptView) Backspace() {
	if v == nil || v.cursor <= 0 {
		return
	}
	runes := []rune(v.Text)
	if v.cursor > len(runes) {
		v.cursor = len(runes)
	}
	next := make([]rune, 0, len(runes)-1)
	next = append(next, runes[:v.cursor-1]...)
	next = append(next, runes[v.cursor:]...)
	v.Text = string(next)
	v.cursor--
}

func (v *CustomPromptView) Cursor() int {
	if v == nil {
		return 0
	}
	return v.cursor
}

// MoveCursor moves the cursor by delta runes, clamping at the text bounds.
func (v *CustomPromptView) MoveCursor(delta int) {
	if v == nil {
		return
	}
	runes := []rune(v.Text)
	next := v.cursor + delta
	if next < 0 {
		next = 0
	}
	if next > len(runes) {
		next = len(runes)
	}
	v.cursor = next
}

func (v *CustomPromptView) MoveLine(delta int) {
	if v == nil {
		return
	}
	lines := strings.Split(v.Text, "\n")
	row := v.lineAtCursor()
	target := row + delta
	if target < 0 {
		target = 0
	}
	if target >= len(lines) {
		target = len(lines) - 1
	}
	if target == row {
		return
	}
	col := v.cursor - v.lineStartOffset(row)
	if col > len(lines[target]) {
		col = len(lines[target])
	}
	v.cursor = v.lineStartOffset(target) + col
}

func (v *CustomPromptView) MoveLineStart() {
	if v == nil {
		return
	}
	v.cursor = v.lineStartOffset(v.lineAtCursor())
}

func (v *CustomPromptView) MoveLineEnd() {
	if v == nil {
		return
	}
	row := v.lineAtCursor()
	lines := strings.Split(v.Text, "\n")
	if row < len(lines) {
		v.cursor = v.lineStartOffset(row) + len(lines[row])
	}
}

func (v *CustomPromptView) MoveWord(direction int) {
	if v == nil {
		return
	}
	runes := []rune(v.Text)
	if len(runes) == 0 {
		return
	}
	index := v.cursor
	if direction > 0 {
		for index < len(runes) && unicode.IsSpace(runes[index]) {
			index++
		}
		for index < len(runes) && !unicode.IsSpace(runes[index]) {
			index++
		}
		for index < len(runes) && unicode.IsSpace(runes[index]) {
			index++
		}
	} else {
		for index > 0 && unicode.IsSpace(runes[index-1]) {
			index--
		}
		for index > 0 && !unicode.IsSpace(runes[index-1]) {
			index--
		}
	}
	v.cursor = index
}

func (v *CustomPromptView) MoveWordEnd() {
	if v == nil {
		return
	}
	runes := []rune(v.Text)
	if len(runes) == 0 {
		return
	}
	index := v.cursor
	for index < len(runes) && !unicode.IsSpace(runes[index]) {
		index++
	}
	if index < len(runes) && index > 0 && !unicode.IsSpace(runes[index]) {
		index++
	}
	v.cursor = index
}

func (v *CustomPromptView) DeleteCharAtCursor() {
	if v == nil {
		return
	}
	runes := []rune(v.Text)
	if v.cursor >= len(runes) {
		return
	}
	v.Text = string(append(append([]rune(nil), runes[:v.cursor]...), runes[v.cursor+1:]...))
}

func (v *CustomPromptView) ReplaceCharAtCursor(ch rune) {
	if v == nil {
		return
	}
	runes := []rune(v.Text)
	if v.cursor >= len(runes) {
		return
	}
	runes[v.cursor] = ch
	v.Text = string(runes)
}

func (v *CustomPromptView) DeleteWordBackward() {
	if v == nil {
		return
	}
	start := v.cursor
	runes := []rune(v.Text)
	for start > 0 && unicode.IsSpace(runes[start-1]) {
		start--
	}
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	v.Text = string(append(append([]rune(nil), runes[:start]...), runes[v.cursor:]...))
	v.cursor = start
}

func (v *CustomPromptView) DeleteWordForward() {
	if v == nil {
		return
	}
	runes := []rune(v.Text)
	end := v.cursor
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}
	for end < len(runes) && unicode.IsSpace(runes[end]) {
		end++
	}
	v.Text = string(append(append([]rune(nil), runes[:v.cursor]...), runes[end:]...))
}

func (v *CustomPromptView) KillLineStart() {
	if v == nil {
		return
	}
	start := v.lineStartOffset(v.lineAtCursor())
	runes := []rune(v.Text)
	v.Text = string(append(append([]rune(nil), runes[:start]...), runes[v.cursor:]...))
	v.cursor = start
}

func (v *CustomPromptView) KillLineEnd() {
	if v == nil {
		return
	}
	row := v.lineAtCursor()
	end := v.lineStartOffset(row) + len(strings.Split(v.Text, "\n")[row])
	runes := []rune(v.Text)
	v.Text = string(append(append([]rune(nil), runes[:v.cursor]...), runes[end:]...))
}

func (v *CustomPromptView) DeleteLine() {
	if v == nil {
		return
	}
	lines := strings.Split(v.Text, "\n")
	row := v.lineAtCursor()
	if row < 0 || row >= len(lines) {
		return
	}
	next := append([]string(nil), lines[:row]...)
	if row+1 < len(lines) {
		next = append(next, lines[row+1:]...)
	}
	v.Text = strings.Join(next, "\n")
	if v.cursor > len([]rune(v.Text)) {
		v.cursor = len([]rune(v.Text))
	}
}

func (v *CustomPromptView) lineAtCursor() int {
	if v == nil {
		return 0
	}
	row := 0
	runes := []rune(v.Text)
	limit := v.cursor
	if limit > len(runes) {
		limit = len(runes)
	}
	for index := 0; index < limit; index++ {
		if runes[index] == '\n' {
			row++
		}
	}
	return row
}

func (v *CustomPromptView) lineStartOffset(row int) int {
	if v == nil {
		return 0
	}
	runes := []rune(v.Text)
	offset := 0
	current := 0
	for index := 0; index < len(runes) && current < row; index++ {
		if runes[index] == '\n' {
			current++
			offset = index + 1
		}
	}
	return offset
}

func (v *CustomPromptView) Rows() []string {
	if v == nil {
		return nil
	}
	rows := []string{v.Title}
	if strings.TrimSpace(v.ContextLabel) != "" {
		rows = append(rows, v.ContextLabel)
	}
	if v.Text == "" {
		rows = append(rows, v.Placeholder)
	} else {
		rows = append(rows, strings.Split(v.Text, "\n")...)
	}
	rows = append(rows, "")
	if v.vimEnabled {
		if v.vimInsert {
			rows = append(rows, "Press enter to confirm or esc to enter normal mode")
		} else {
			rows = append(rows, "Vim normal · i to insert · esc to cancel")
		}
	} else {
		rows = append(rows, "Press enter to submit or esc to cancel")
	}
	return rows
}

func normalizeCustomPromptKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "+", "-")
	return key
}
