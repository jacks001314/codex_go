package tea

import (
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
