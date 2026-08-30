package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// These tests verify the Vim normal mode implemented in the tea Model
// (tui/tea/vim_mode.go), mirroring Rust's bottom_pane/chat_composer.rs Vim
// support: /vim starts the composer in normal mode, normal-mode keys dispatch
// the registered vim_normal actions (motions, insert-entering, edits, line
// yank/paste, dd/yy), insert mode types normally until Esc, and the #38907
// queued-message history-up path only fires in normal mode.

func vimTestModel(value string) *Model {
	m := NewModel(codextui.NewState(nil), Options{Width: 200, Height: 36})
	m.vimMode = true
	m.vimInsert = false
	if value != "" {
		m.composer.SetValue(value)
		m.composer.SetCursor(0)
	}
	return m
}

func vimCursorColumn(m *Model) int {
	info := m.composer.LineInfo()
	return info.StartColumn + info.ColumnOffset
}

func vimKeyPress(m *Model, r rune) *Model {
	updated, _ := m.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{r}})
	return updated.(*Model)
}

func vimEscape(m *Model) *Model {
	updated, _ := m.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	return updated.(*Model)
}

func vimEnter(m *Model) *Model {
	updated, _ := m.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	return updated.(*Model)
}

// TestVimSearchMotionsLikeRust pins the composer search motions (#41586): `/`
// enters forward search, typed runes accumulate into the query, Enter lands on
// the first match relative to the cursor, and n/N navigate (wrapping).
func TestVimSearchMotionsLikeRust(t *testing.T) {
	m := vimTestModel("hello world hello")
	m = vimKeyPress(m, '/')
	if !m.vimSearchMode {
		t.Fatalf("slash did not enter search mode")
	}
	for _, r := range "hello" {
		m = vimKeyPress(m, r)
	}
	if m.vimSearchQuery != "hello" {
		t.Fatalf("query = %q, want hello", m.vimSearchQuery)
	}
	m = vimEnter(m)
	if m.vimSearchMode {
		t.Fatal("enter did not exit search mode")
	}
	if got := m.vimValueByteOffset(); got != 0 {
		t.Fatalf("cursor after search = %d, want 0", got)
	}
	// n moves to the second "hello" at byte offset 12.
	m = vimKeyPress(m, 'n')
	if got := m.vimValueByteOffset(); got != 12 {
		t.Fatalf("cursor after n = %d, want 12", got)
	}
	// N moves back to the first match.
	m = vimKeyPress(m, 'N')
	if got := m.vimValueByteOffset(); got != 0 {
		t.Fatalf("cursor after N = %d, want 0", got)
	}
	// A backward search (?) lands on the last match before the cursor.
	m = vimKeyPress(m, '?')
	if !m.vimSearchMode {
		t.Fatal("question mark did not enter search mode")
	}
	for _, r := range "hello" {
		m = vimKeyPress(m, r)
	}
	m = vimEnter(m)
	if got := m.vimValueByteOffset(); got != 12 {
		t.Fatalf("cursor after backward search = %d, want 12", got)
	}
	// Esc in search mode cancels it.
	m = vimKeyPress(m, '/')
	if !m.vimSearchMode {
		t.Fatal("slash did not enter search mode (second)")
	}
	m = vimEscape(m)
	if m.vimSearchMode || m.vimSearchQuery != "" {
		t.Fatalf("esc did not cancel search: mode=%v query=%q", m.vimSearchMode, m.vimSearchQuery)
	}
}

// TestVimSearchQueryFooterLikeRust pins the live search-query footer (#41586): a
// forward search renders "/" + the typed query, and a backward search renders "?".
func TestVimSearchQueryFooterLikeRust(t *testing.T) {
	m := vimTestModel("")
	m = vimKeyPress(m, '/')
	for _, r := range "hello" {
		m = vimKeyPress(m, r)
	}
	view := m.View()
	if !strings.Contains(view, "/hello") {
		t.Fatalf("view does not show the forward search footer: %q", view)
	}
	m = vimEscape(m)
	m = vimKeyPress(m, '?')
	for _, r := range "world" {
		m = vimKeyPress(m, r)
	}
	view = m.View()
	if !strings.Contains(view, "?world") {
		t.Fatalf("view does not show the backward search footer: %q", view)
	}
}

// TestVimModeToggleStartsInNormalModeAndInsertTypes pins the mode state
// machine: /vim starts normal; i enters insert where keys type (even k, which
// is a normal-mode motion); Esc returns to normal where x deletes.
func TestVimModeToggleStartsInNormalModeAndInsertTypes(t *testing.T) {
	m := vimTestModel("")
	if !m.vimMode || m.vimInsert {
		t.Fatalf("vimMode=%v vimInsert=%v, want enabled + normal mode", m.vimMode, m.vimInsert)
	}
	m = vimKeyPress(m, 'i')
	if !m.vimInsert {
		t.Fatal("i did not enter insert mode")
	}
	m = vimKeyPress(m, 'k')
	if got := m.composer.Value(); got != "k" {
		t.Fatalf("composer after insert-mode k = %q, want k (insert types)", got)
	}
	m = vimEscape(m)
	if m.vimInsert {
		t.Fatal("esc did not return to normal mode")
	}
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'x')
	if got := m.composer.Value(); got != "" {
		t.Fatalf("composer after x = %q, want empty (delete_char)", got)
	}
}

// TestVimOperatorLineMotionsDeleteYankAdjacentLinesLikeVim pins dj / dk / yj:
// the j/k motions act on the current line plus its neighbor.
func TestVimOperatorLineMotionsDeleteYankAdjacentLinesLikeVim(t *testing.T) {
	m := vimTestModel("line zero\nline one\nline two")
	m.composer.CursorUp()
	m.composer.CursorUp()
	m.composer.SetCursor(0) // cursor on line zero (row 0)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'j')
	if got := m.composer.Value(); got != "line two" {
		t.Fatalf("dj value = %q, want line two (line zero + line one deleted)", got)
	}

	m = vimTestModel("line zero\nline one\nline two")
	m.composer.CursorUp()
	m.composer.SetCursor(0) // cursor on line one (row 1)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'k')
	if got := m.composer.Value(); got != "line two" {
		t.Fatalf("dk value = %q, want line two (line zero + line one deleted)", got)
	}

	m = vimTestModel("line zero\nline one\nline two")
	m.composer.SetCursor(0) // cursor on line two (row 2, the last)
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, 'j')
	if got := m.vimYank; got != "line two" {
		t.Fatalf("yj yank at last line = %q, want line two", got)
	}

	m = vimTestModel("line zero\nline one\nline two")
	m.composer.SetCursor(0) // cursor on line two (row 2)
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, 'k')
	if got := m.vimYank; got != "line one\nline two" {
		t.Fatalf("yk yank = %q, want line one\\nline two", got)
	}
}

// TestVimBigWordTextObjectLikeVim pins the W (big word) inner text object,
// which targets the whitespace-delimited word like the plain w object.
func TestVimBigWordTextObjectLikeVim(t *testing.T) {
	m := vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'i')
	m = vimKeyPress(m, 'W')
	if got := m.composer.Value(); got != "hello  world" {
		t.Fatalf("diW value = %q, want hello  world (inner big word)", got)
	}
}

// TestVimOperatorMotionsDeleteYankChangeLikeVim pins d/y/c applied to
// horizontal motions: dw / dh / d0 / d$ / c$ / yw.
func TestVimOperatorMotionsDeleteYankChangeLikeVim(t *testing.T) {
	// dw deletes from the cursor to the start of the next word.
	m := vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'w')
	if got := m.composer.Value(); got != "hello world" {
		t.Fatalf("dw value = %q, want hello world", got)
	}

	// dh deletes the character left of the cursor.
	m = vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'h')
	if got := m.composer.Value(); got != "hellobrave world" {
		t.Fatalf("dh value = %q, want hellobrave world", got)
	}

	// d0 deletes to the start of the line; d$ to the end.
	m = vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, '0')
	if got := m.composer.Value(); got != "brave world" {
		t.Fatalf("d0 value = %q, want brave world", got)
	}
	m = vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, '$')
	if got := m.composer.Value(); got != "hello " {
		t.Fatalf("d$ value = %q, want hello ", got)
	}

	// c$ deletes to the line end and enters insert mode.
	m = vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'c')
	m = vimKeyPress(m, '$')
	if got := m.composer.Value(); got != "hello " || !m.vimInsert {
		t.Fatalf("c$ value = %q insert=%v, want hello  + insert", got, m.vimInsert)
	}

	// yw yanks from the cursor to the next word start.
	m = vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, 'w')
	if got := m.vimYank; got != "brave " {
		t.Fatalf("yw yank = %q, want brave ", got)
	}
}

// TestVimOperatorTextObjectsDeleteInnerAroundParensLikeVim pins diw / daw /
// di( and the yank-paste path (yiw + p).
func TestVimOperatorTextObjectsDeleteInnerAroundParensLikeVim(t *testing.T) {
	m := vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'i')
	m = vimKeyPress(m, 'w')
	if got := m.composer.Value(); got != "hello  world" {
		t.Fatalf("diw value = %q, want hello  world (inner word)", got)
	}

	m = vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'a')
	m = vimKeyPress(m, 'w')
	if got := m.composer.Value(); got != "helloworld" {
		t.Fatalf("daw value = %q, want helloworld (around word)", got)
	}

	m = vimTestModel("call(foo, bar)")
	m.composer.SetCursor(7)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'i')
	m = vimKeyPress(m, '(')
	if got := m.composer.Value(); got != "call()" {
		t.Fatalf("di( value = %q, want call()", got)
	}

	m = vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, 'i')
	m = vimKeyPress(m, 'w')
	if got := m.vimYank; got != "brave" {
		t.Fatalf("yiw yank = %q, want brave", got)
	}
	m = vimKeyPress(m, 'p')
	if got := m.composer.Value(); got != "hello bravebrave world" {
		t.Fatalf("yiw+p value = %q, want hello bravebrave world (paste after cursor)", got)
	}
}

// TestVimLineOperatorRepeatsAndEscCancel pins cc / yy and operator cancel.
func TestVimLineOperatorRepeatsAndEscCancel(t *testing.T) {
	m := vimTestModel("line one\nline two")
	m.composer.CursorUp()
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'c')
	m = vimKeyPress(m, 'c')
	if got := m.composer.Value(); got != "line two" || !m.vimInsert {
		t.Fatalf("cc value = %q insert=%v, want line two + insert", got, m.vimInsert)
	}

	m = vimTestModel("alpha beta")
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, 'y')
	if got := m.vimYank; got != "alpha beta" {
		t.Fatalf("yy yank = %q, want alpha beta", got)
	}

	// Esc cancels a pending operator; the following w is then a plain motion.
	m = vimTestModel("hello brave world")
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'd')
	m = vimEscape(m)
	m = vimKeyPress(m, 'w')
	if got := m.composer.Value(); got != "hello brave world" {
		t.Fatalf("cancelled operator mutated value: %q", got)
	}
}

// TestVimMotionsMoveCursorLikeVim pins h/l/w/b/e/0/$ on a single line.
func TestVimMotionsMoveCursorLikeVim(t *testing.T) {
	m := vimTestModel("alpha beta gamma")
	m.composer.SetCursor(0)
	if got := vimCursorColumn(m); got != 0 {
		t.Fatalf("initial cursor = %d, want 0", got)
	}
	// h on a single line does not move; l moves right one.
	m = vimKeyPress(m, 'h')
	if got := vimCursorColumn(m); got != 0 {
		t.Fatalf("h cursor = %d, want 0", got)
	}
	m = vimKeyPress(m, 'l')
	if got := vimCursorColumn(m); got != 1 {
		t.Fatalf("l cursor = %d, want 1", got)
	}
	// w: start of beta (byte 6), then gamma (byte 11).
	m = vimKeyPress(m, 'w')
	if got := vimCursorColumn(m); got != 6 {
		t.Fatalf("w cursor = %d, want 6 (start of beta)", got)
	}
	m = vimKeyPress(m, 'w')
	if got := vimCursorColumn(m); got != 11 {
		t.Fatalf("w cursor = %d, want 11 (start of gamma)", got)
	}
	// b: start of beta (byte 6).
	m = vimKeyPress(m, 'b')
	if got := vimCursorColumn(m); got != 6 {
		t.Fatalf("b cursor = %d, want 6", got)
	}
	// e: end of beta (byte 9).
	m = vimKeyPress(m, 'e')
	if got := vimCursorColumn(m); got != 9 {
		t.Fatalf("e cursor = %d, want 9 (end of beta)", got)
	}
	// 0 / $.
	m = vimKeyPress(m, '0')
	if got := vimCursorColumn(m); got != 0 {
		t.Fatalf("0 cursor = %d, want 0", got)
	}
	m = vimKeyPress(m, '$')
	if got := vimCursorColumn(m); got != 16 {
		t.Fatalf("$ cursor = %d, want 16 (line end)", got)
	}
}

// TestVimFindTillMotionsLikeVim pins #40785 f/F/t/T character find and till
// motions (line-local): f lands on the target, t stops just before it, F/T
// work backward, and d f applies the delete operator over the found range.
func TestVimFindTillMotionsLikeVim(t *testing.T) {
	m := vimTestModel("hello world")
	m = vimKeyPress(m, 'f')
	m = vimKeyPress(m, 'o')
	if got := vimCursorColumn(m); got != 4 {
		t.Fatalf("f then o cursor = %d, want 4 (on the 'o')", got)
	}

	m = vimTestModel("hello world")
	m = vimKeyPress(m, 't')
	m = vimKeyPress(m, 'o')
	if got := vimCursorColumn(m); got != 3 {
		t.Fatalf("t then o cursor = %d, want 3 (just before the 'o')", got)
	}

	m = vimTestModel("hello world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'F')
	m = vimKeyPress(m, 'o')
	if got := vimCursorColumn(m); got != 4 {
		t.Fatalf("F then o cursor = %d, want 4 (on the previous 'o')", got)
	}

	m = vimTestModel("hello world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'T')
	m = vimKeyPress(m, 'o')
	if got := vimCursorColumn(m); got != 5 {
		t.Fatalf("T then o cursor = %d, want 5 (just after the previous 'o')", got)
	}

	m = vimTestModel("hello world")
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'f')
	m = vimKeyPress(m, 'o')
	if got := m.composer.Value(); got != " world" {
		t.Fatalf("d f o value = %q, want ' world' (deleted hello)", got)
	}
}

// TestVimEditsDeleteAndChangeLikeVim pins x / D / s / C.
func TestVimEditsDeleteAndChangeLikeVim(t *testing.T) {
	m := vimTestModel("hello world")
	m = vimKeyPress(m, 'x')
	if got := m.composer.Value(); got != "ello world" {
		t.Fatalf("x value = %q, want ello world", got)
	}

	m = vimTestModel("hello world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'D')
	if got := m.composer.Value(); got != "hello " {
		t.Fatalf("D value = %q, want hello ", got)
	}

	m = vimTestModel("hello world")
	m = vimKeyPress(m, 's')
	if got := m.composer.Value(); got != "ello world" || !m.vimInsert {
		t.Fatalf("s value = %q insert=%v, want ello world + insert", got, m.vimInsert)
	}

	m = vimTestModel("hello world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'C')
	if got := m.composer.Value(); got != "hello " || !m.vimInsert {
		t.Fatalf("C value = %q insert=%v, want hello  + insert", got, m.vimInsert)
	}
}

// TestVimReplaceCharAndChangeMotionsLikeVim pins #39661: `r` replaces the
// grapheme under the cursor while staying in normal mode, Esc cancels a
// pending replacement, and cw / cj / ck change motions work alongside cc.
func TestVimReplaceCharAndChangeMotionsLikeVim(t *testing.T) {
	m := vimTestModel("hello world")
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'r')
	m = vimKeyPress(m, 'H')
	if got := m.composer.Value(); got != "Hello world" {
		t.Fatalf("rH value = %q, want Hello world", got)
	}
	if m.vimInsert || m.vimPendingReplace {
		t.Fatalf("replace_char should stay in normal mode: insert=%v pending=%v", m.vimInsert, m.vimPendingReplace)
	}

	// Esc cancels a pending replacement without mutating the composer.
	m = vimTestModel("hello world")
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'r')
	m = vimEscape(m)
	if got := m.composer.Value(); got != "hello world" || m.vimPendingReplace {
		t.Fatalf("cancelled replacement value = %q pending=%v", got, m.vimPendingReplace)
	}

	// cw changes from the cursor to the next word start and enters insert.
	m = vimTestModel("hello brave world")
	m.composer.SetCursor(6)
	m = vimKeyPress(m, 'c')
	m = vimKeyPress(m, 'w')
	if got := m.composer.Value(); got != "hello world" || !m.vimInsert {
		t.Fatalf("cw value = %q insert=%v, want hello world + insert", got, m.vimInsert)
	}

	// cj changes the current line plus the next line.
	m = vimTestModel("line one\nline two\nline three")
	m.composer.CursorUp()
	m.composer.CursorUp()
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'c')
	m = vimKeyPress(m, 'j')
	if got := m.composer.Value(); got != "line three" || !m.vimInsert {
		t.Fatalf("cj value = %q insert=%v, want line three + insert", got, m.vimInsert)
	}
}

// TestVimLineYankPasteAndOperators pins Y/p and dd/yy.
func TestVimLineYankPasteAndOperators(t *testing.T) {
	m := vimTestModel("alpha beta")
	m = vimKeyPress(m, 'Y')
	m = vimKeyPress(m, 'p')
	if got := m.composer.Value(); got != "alpha betaalpha beta" {
		t.Fatalf("Y+p value = %q", got)
	}

	m = vimTestModel("line one\nline two\nline three")
	m.composer.CursorUp()
	m.composer.CursorUp()
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'd')
	if got := m.composer.Value(); got != "line two\nline three" {
		t.Fatalf("dd value = %q", got)
	}

	m = vimTestModel("line one\nline two")
	m.composer.CursorUp()
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, 'y')
	if got := m.vimYank; got != "line one" {
		t.Fatalf("yy yank = %q, want line one", got)
	}
}

// TestVimInsertModeKeepsQueuedMessageIntactLikeRust pins the #38907 gate: the
// queued-message history-up restore only fires in Vim NORMAL mode. In insert
// mode k types; back in normal mode on an empty composer k restores the
// queued follow-up.
func TestVimInsertModeKeepsQueuedMessageIntactLikeRust(t *testing.T) {
	m := vimTestModel("queued message")
	m.queueComposer(false)
	m.composer.SetValue("")
	m = vimKeyPress(m, 'i')
	m = vimKeyPress(m, 'k')
	if got := m.composer.Value(); got != "k" {
		t.Fatalf("insert-mode k = %q, want k typed", got)
	}
	if len(m.queued) != 1 {
		t.Fatalf("queue changed in insert mode: %#v", m.queued)
	}
	m = vimEscape(m)
	m.composer.SetValue("")
	m = vimKeyPress(m, 'k')
	if got := m.composer.Value(); got != "queued message" {
		t.Fatalf("normal-mode k = %q, want queued message restored", got)
	}
	if len(m.queued) != 0 {
		t.Fatalf("queue after normal-mode restore = %d, want 0", len(m.queued))
	}
}
func TestVimDotRepeatReplaysDeleteWordLikeRust(t *testing.T) {
	m := vimTestModel("alpha beta gamma")
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'w')
	if got := m.composer.Value(); got != "beta gamma" {
		t.Fatalf("after dw = %q, want beta gamma", got)
	}
	m = vimKeyPress(m, '.')
	if got := m.composer.Value(); got != "gamma" {
		t.Fatalf("after dot = %q, want gamma", got)
	}
}

func TestVimDotRepeatReplaysDeleteCharLikeRust(t *testing.T) {
	m := vimTestModel("abc")
	m = vimKeyPress(m, 'x')
	if got := m.composer.Value(); got != "bc" {
		t.Fatalf("after x = %q, want bc", got)
	}
	m = vimKeyPress(m, '.')
	if got := m.composer.Value(); got != "c" {
		t.Fatalf("after dot = %q, want c", got)
	}
}

// TestVimBufferJumpMotionsLikeRust pins #40958 gg / G buffer line jumps: gg
// moves to the first line's first non-blank, G to the last line's.
func TestVimBufferJumpMotionsLikeRust(t *testing.T) {
	// gg jumps to the top buffer line. vimTestModel leaves the cursor on the
	// last row, so this exercises the jump from the bottom.
	m := vimTestModel("  alpha\n beta\n  gamma")
	m = vimKeyPress(m, 'g')
	m = vimKeyPress(m, 'g')
	if got := vimCursorColumn(m); got != 2 {
		t.Fatalf("gg cursor column = %d, want 2 (first non-blank of first line)", got)
	}
	if got := m.composer.Line(); got != 0 {
		t.Fatalf("gg line = %d, want 0", got)
	}

	// G jumps to the last buffer line's first non-blank from the top.
	m = vimTestModel("  alpha\n beta\n  gamma")
	m.composer.CursorUp()
	m.composer.CursorUp()
	m.composer.SetCursor(0)
	m = vimKeyPress(m, 'G')
	if got := m.composer.Line(); got != 2 {
		t.Fatalf("G line = %d, want 2", got)
	}
	if got := vimCursorColumn(m); got != 2 {
		t.Fatalf("G cursor column = %d, want 2 (first non-blank of last line)", got)
	}
}

// TestVimBufferJumpOperatorMotionsLikeRust pins the operator forms: dgg
// deletes the buffer start through the end of the current line; dG / yG / cG
// apply over the current line through the end of the buffer; and an
// incomplete gg chord is cancelled by another key.
func TestVimBufferJumpOperatorMotionsLikeRust(t *testing.T) {
	// dgg on line one deletes from the buffer start through line one.
	m := vimTestModel("line zero\nline one\nline two")
	m.composer.CursorUp() // row 1 (line one)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'g')
	m = vimKeyPress(m, 'g')
	if got := m.composer.Value(); got != "line two" {
		t.Fatalf("dgg value = %q, want line two", got)
	}

	// dG on line one deletes line one through the end of the buffer.
	m = vimTestModel("line zero\nline one\nline two")
	m.composer.CursorUp() // row 1 (line one)
	m = vimKeyPress(m, 'd')
	m = vimKeyPress(m, 'G')
	if got := m.composer.Value(); got != "line zero\n" {
		t.Fatalf("dG value = %q, want line zero\\n", got)
	}

	// yG yanks the current line through the end of the buffer.
	m = vimTestModel("line zero\nline one\nline two")
	m.composer.CursorUp() // row 1 (line one)
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, 'G')
	if got := m.vimYank; got != "line one\nline two" {
		t.Fatalf("yG yank = %q, want line one\\nline two", got)
	}

	// ygg yanks the buffer start through the end of the current line.
	m = vimTestModel("line zero\nline one\nline two")
	m.composer.CursorUp() // row 1 (line one)
	m = vimKeyPress(m, 'y')
	m = vimKeyPress(m, 'g')
	m = vimKeyPress(m, 'g')
	if got := m.vimYank; got != "line zero\nline one\n" {
		t.Fatalf("ygg yank = %q, want line zero\\nline one\\n", got)
	}

	// cG changes through the end of the buffer and enters insert mode.
	m = vimTestModel("line zero\nline one\nline two")
	m.composer.CursorUp() // row 1 (line one)
	m = vimKeyPress(m, 'c')
	m = vimKeyPress(m, 'G')
	if got := m.composer.Value(); got != "line zero\n" || !m.vimInsert {
		t.Fatalf("cG value = %q insert=%v, want line zero\\n + insert", got, m.vimInsert)
	}

	// An incomplete gg chord is cancelled by x, which then deletes a char.
	m = vimTestModel("abc")
	m = vimKeyPress(m, 'g')
	m = vimKeyPress(m, 'x')
	if got := m.composer.Value(); got != "bc" {
		t.Fatalf("g then x value = %q, want bc (chord cancelled, x deletes)", got)
	}
	if m.vimPendingG {
		t.Fatal("pending g chord not cleared after a non-g key")
	}
}
