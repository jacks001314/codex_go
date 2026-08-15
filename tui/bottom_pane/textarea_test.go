package bottompane

import (
	"reflect"
	"testing"
)

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

func TestTextAreaStateDeletesThaiCombiningMarksOneAtATimeLikeRust(t *testing.T) {
	// Rust #38662: backspace removes Thai vowel and tone marks individually
	// instead of deleting the whole grapheme cluster. Go's backward deletion is
	// rune-bounded, which already yields the same observable sequence.
	state := NewTextAreaState("ที่")
	state.SetCursor(len(state.Text))

	state.DeleteBackward(1)
	if state.Text != "ที" {
		t.Fatalf("after first backspace text = %q, want %q", state.Text, "ที")
	}
	state.DeleteBackward(1)
	if state.Text != "ท" {
		t.Fatalf("after second backspace text = %q, want %q", state.Text, "ท")
	}
	state.DeleteBackward(1)
	if state.Text != "" {
		t.Fatalf("after third backspace text = %q, want empty", state.Text)
	}

	// Non-Thai grapheme behavior stays rune-bounded; spacing vowel "ซ้ำ" deletes
	// the final mark first like Rust.
	spacing := NewTextAreaState("ซ้ำ")
	spacing.SetCursor(len(spacing.Text))
	spacing.DeleteBackward(1)
	if spacing.Text != "ซ้" {
		t.Fatalf("spacing vowel text = %q, want %q", spacing.Text, "ซ้")
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

func TestTextAreaWrapAndCursorUseGraphemeCellWidths(t *testing.T) {
	state := NewTextAreaState("ab\uff76\uff9ec")
	// Rust ad6e48ddd3: a logical line that exactly fills the width reserves a
	// continuation row so the insertion point stays visible.
	if got, want := state.WrappedLines(3), []string{"ab", "\uff76\uff9ec", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("halfwidth WrappedLines = %#v, want %#v", got, want)
	}
	col, row := state.CursorPosition(3, 0)
	if col != 0 || row != 2 {
		t.Fatalf("halfwidth cursor col=%d row=%d, want 0,2", col, row)
	}

	state = NewTextAreaState("a\U0001f44d\U0001f3fbb")
	// U+1F44D + U+1F3FB form one grapheme; the trailing "b" does not fill the
	// width, so no continuation row is reserved.
	if got, want := state.WrappedLines(2), []string{"a", "\U0001f44d\U0001f3fb", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("emoji WrappedLines = %#v, want %#v", got, want)
	}
	col, row = state.CursorPosition(2, 0)
	if col != 1 || row != 2 {
		t.Fatalf("emoji cursor col=%d row=%d, want 1,2", col, row)
	}
}

func TestTextAreaFullLinesReserveVisibleCursorRowsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		text   string
		width  int
		lines  []string
		cursor int
		col    int
		row    int
	}{
		{text: "abad", width: 4, lines: []string{"abad", ""}, cursor: 4, col: 0, row: 1},
		{text: "界界", width: 4, lines: []string{"界界", ""}, cursor: 6, col: 0, row: 1},
		{text: "ab\uff76\uff9e", width: 4, lines: []string{"ab\uff76\uff9e", ""}, cursor: 8, col: 0, row: 1},
		{text: "abad\nef", width: 4, lines: []string{"abad", "", "ef"}, cursor: 4, col: 0, row: 1},
		{text: "abad\nef", width: 4, lines: []string{"abad", "", "ef"}, cursor: 5, col: 0, row: 2},
	} {
		state := NewTextAreaState(tc.text)
		state.SetCursor(min(tc.cursor, len(state.Text)))
		if got := state.WrappedLines(tc.width); !reflect.DeepEqual(got, tc.lines) {
			t.Fatalf("%q WrappedLines(%d) = %#v, want %#v", tc.text, tc.width, got, tc.lines)
		}
		if got := state.DesiredHeight(tc.width); got != len(tc.lines) {
			t.Fatalf("%q DesiredHeight(%d) = %d, want %d", tc.text, tc.width, got, len(tc.lines))
		}
		col, row := state.CursorPosition(tc.width, 0)
		if col != tc.col || row != tc.row {
			t.Fatalf("%q cursor(%d) = %d,%d, want %d,%d", tc.text, tc.cursor, col, row, tc.col, tc.row)
		}
	}
}

func TestTextAreaTrailingSpacesWrapWithoutCursorEscapeLikeRust(t *testing.T) {
	state := NewTextAreaState("abad        ")
	state.SetCursor(len(state.Text))
	lines := state.WrappedLines(5)
	if len(lines) != 3 {
		t.Fatalf("trailing-space WrappedLines = %#v, want 3 rows (Rust ad6e48ddd3)", lines)
	}
	col, row := state.CursorPosition(5, 0)
	if col >= 5 || row < 0 {
		t.Fatalf("trailing-space cursor = %d,%d, want inside viewport", col, row)
	}
}
