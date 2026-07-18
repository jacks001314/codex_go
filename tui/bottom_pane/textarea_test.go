package bottompane

import "testing"

func TestTextAreaStateEditsClampToUTF8Boundaries(t *testing.T) {
	world := "\u4e16\u754c"
	state := NewTextAreaState("hi " + world)
	state.SetCursor(len("hi ") + len("\u4e16"))
	state.MoveCursorLeft()
	if state.Cursor != len("hi ") {
		t.Fatalf("cursor after left = %d", state.Cursor)
	}
	state.InsertString("big ")
	if state.Text != "hi big "+world {
		t.Fatalf("text after insert = %q", state.Text)
	}
	state.SetCursor(len("hi big \u4e16") - 1)
	if state.Cursor != len("hi big ") {
		t.Fatalf("cursor should clamp before partial rune, got %d", state.Cursor)
	}
	state.DeleteForward(1)
	if state.Text != "hi big \u754c" {
		t.Fatalf("text after delete forward = %q", state.Text)
	}
}

func TestTextAreaStateKillYankAndWordDeleteMatchRustCoreBehavior(t *testing.T) {
	state := NewTextAreaState("one two three\nnext")
	state.SetCursor(len("one two"))
	state.DeleteBackwardWord()
	if state.Text != "one  three\nnext" || state.Cursor != len("one ") || state.KillBuffer != "two" {
		t.Fatalf("after delete word text=%q cursor=%d kill=%q", state.Text, state.Cursor, state.KillBuffer)
	}
	state.KillToEndOfLine()
	if state.Text != "one \nnext" || state.KillBuffer != " three" {
		t.Fatalf("after kill eol text=%q kill=%q", state.Text, state.KillBuffer)
	}
	state.Yank()
	if state.Text != "one  three\nnext" {
		t.Fatalf("after yank text=%q", state.Text)
	}
	state.MoveCursorToEndOfLine()
	state.KillToBeginningOfLine()
	if state.Text != "\nnext" || state.KillBuffer != "one  three" {
		t.Fatalf("after kill bol text=%q kill=%q", state.Text, state.KillBuffer)
	}
}

func TestTextAreaStateWordDeletesRespectRustSeparators(t *testing.T) {
	state := NewTextAreaState("path/to/file")
	state.SetCursor(len(state.Text))
	state.DeleteBackwardWord()
	if state.Text != "path/to/" || state.Cursor != len(state.Text) || state.KillBuffer != "file" {
		t.Fatalf("delete backward file text=%q cursor=%d kill=%q", state.Text, state.Cursor, state.KillBuffer)
	}
	state.DeleteBackwardWord()
	if state.Text != "path/to" || state.Cursor != len(state.Text) || state.KillBuffer != "/" {
		t.Fatalf("delete backward slash text=%q cursor=%d kill=%q", state.Text, state.Cursor, state.KillBuffer)
	}

	state = NewTextAreaState("foo/ ")
	state.SetCursor(len(state.Text))
	state.DeleteBackwardWord()
	if state.Text != "foo" || state.Cursor != 3 || state.KillBuffer != "/ " {
		t.Fatalf("delete backward trailing separator text=%q cursor=%d kill=%q", state.Text, state.Cursor, state.KillBuffer)
	}

	state = NewTextAreaState("path/to/file")
	state.SetCursor(0)
	state.DeleteForwardWord()
	if state.Text != "/to/file" || state.Cursor != 0 || state.KillBuffer != "path" {
		t.Fatalf("delete forward path text=%q cursor=%d kill=%q", state.Text, state.Cursor, state.KillBuffer)
	}
	state.DeleteForwardWord()
	if state.Text != "to/file" || state.Cursor != 0 || state.KillBuffer != "/" {
		t.Fatalf("delete forward slash text=%q cursor=%d kill=%q", state.Text, state.Cursor, state.KillBuffer)
	}

	state = NewTextAreaState("foo\nbar")
	state.SetCursor(3)
	state.DeleteForwardWord()
	if state.Text != "foo" || state.Cursor != 3 || state.KillBuffer != "\nbar" {
		t.Fatalf("delete forward newline text=%q cursor=%d kill=%q", state.Text, state.Cursor, state.KillBuffer)
	}
}

func TestTextAreaStateKillLineBoundaryVariantsMatchRust(t *testing.T) {
	state := NewTextAreaState("abc\ndef")
	state.SetCursor(3)
	state.KillToEndOfLine()
	if state.Text != "abcdef" || state.Cursor != 3 || state.KillBuffer != "\n" {
		t.Fatalf("kill eol newline text=%q cursor=%d kill=%q", state.Text, state.Cursor, state.KillBuffer)
	}

	state = NewTextAreaState("abc\ndef")
	state.SetCursor(4)
	state.KillToBeginningOfLine()
	if state.Text != "abcdef" || state.Cursor != 3 || state.KillBuffer != "\n" {
		t.Fatalf("kill bol newline text=%q cursor=%d kill=%q", state.Text, state.Cursor, state.KillBuffer)
	}
}

func TestTextAreaStateWrapHeightCursorAndHandleKeys(t *testing.T) {
	state := NewTextAreaState("")
	for _, key := range []string{"a", "b", "c", "d", "e", "enter", "f"} {
		state.HandleKey(key)
	}
	if state.Text != "abcde\nf" {
		t.Fatalf("text = %q", state.Text)
	}
	if height := state.DesiredHeight(3); height != 3 {
		t.Fatalf("height = %d", height)
	}
	col, row := state.CursorPosition(3, 2)
	if col != 1 || row != 1 || state.Scroll != 1 {
		t.Fatalf("cursor col=%d row=%d scroll=%d", col, row, state.Scroll)
	}
	state.HandleKey("ctrl+a")
	state.HandleKey("ctrl+k")
	if state.Text != "abcde\n" || state.KillBuffer != "f" {
		t.Fatalf("after key kills text=%q kill=%q", state.Text, state.KillBuffer)
	}
	state.HandleKey("ctrl+y")
	if state.Text != "abcde\nf" {
		t.Fatalf("after yank text=%q", state.Text)
	}
}
