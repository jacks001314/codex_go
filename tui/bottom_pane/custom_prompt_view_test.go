package bottompane

import (
	"testing"
	"time"
)

func TestCustomPromptPasteBurstNewlineDoesNotSubmitShortFirstLine(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		first  string
		second string
	}{
		{"x", "rest"},
		{"id", "body"},
		{"foo", "bar"},
	} {
		view := NewCustomPromptView("Edit goal", "Type a goal objective and press Enter", "", "")
		ms := 0
		for _, ch := range tc.first {
			view.HandleRuneAt(ch, now.Add(time.Duration(ms)*time.Millisecond))
			ms++
		}
		view.HandleKeyAt("enter", now.Add(time.Duration(ms)*time.Millisecond))
		ms++
		for _, ch := range tc.second {
			view.HandleRuneAt(ch, now.Add(time.Duration(ms)*time.Millisecond))
			ms++
		}
		if _, ok := view.LastSubmitted(); ok || view.IsComplete() {
			t.Fatalf("%q/%q submitted early: %#v", tc.first, tc.second, view)
		}
		view.HandleKeyAt("enter", now.Add(200*time.Millisecond))
		want := tc.first + "\n" + tc.second
		if got, ok := view.LastSubmitted(); !ok || got != want || !view.IsComplete() {
			t.Fatalf("submitted = %q ok=%v complete=%v, want %q", got, ok, view.IsComplete(), want)
		}
	}
}

func TestCustomPromptPasteBurstNewlineAfterTabDoesNotSubmit(t *testing.T) {
	view := NewCustomPromptView("Edit goal", "Type a goal objective and press Enter", "", "")
	now := time.Now()
	ms := 0
	view.HandleRuneAt('x', now.Add(time.Duration(ms)*time.Millisecond))
	ms++
	view.HandleKeyAt("tab", now.Add(time.Duration(ms)*time.Millisecond))
	ms++
	view.HandleKeyAt("enter", now.Add(time.Duration(ms)*time.Millisecond))
	ms++
	for _, ch := range "rest" {
		view.HandleRuneAt(ch, now.Add(time.Duration(ms)*time.Millisecond))
		ms++
	}
	if _, ok := view.LastSubmitted(); ok || view.IsComplete() {
		t.Fatalf("submitted early: %#v", view)
	}
	view.HandleKeyAt("enter", now.Add(200*time.Millisecond))
	if got, ok := view.LastSubmitted(); !ok || got != "x\nrest" || !view.IsComplete() {
		t.Fatalf("submitted = %q ok=%v complete=%v", got, ok, view.IsComplete())
	}
}

func TestCustomPromptDelayedEnterAfterTypingSubmits(t *testing.T) {
	view := NewCustomPromptView("Edit goal", "Type a goal objective and press Enter", "", "")
	now := time.Now()
	for idx, ch := range "foo" {
		view.HandleRuneAt(ch, now.Add(time.Duration(idx*20)*time.Millisecond))
	}
	view.HandleKeyAt("enter", now.Add(80*time.Millisecond))
	if got, ok := view.LastSubmitted(); !ok || got != "foo" || !view.IsComplete() {
		t.Fatalf("submitted = %q ok=%v complete=%v", got, ok, view.IsComplete())
	}
}

func TestCustomPromptEmptySubmitCancelPasteAndRows(t *testing.T) {
	view := NewCustomPromptView("Edit goal", "Type a goal objective and press Enter", "", "Review context")
	view.HandleKeyAt("enter", time.Now())
	if view.IsComplete() {
		t.Fatal("empty enter should not complete")
	}
	if rows := view.Rows(); !bottomPaneContainsRow(rows, "Edit goal") || !bottomPaneContainsRow(rows, "Review context") || !bottomPaneContainsRow(rows, "Type a goal objective and press Enter") {
		t.Fatalf("rows = %#v", rows)
	}
	if !view.HandlePaste(" pasted text ") || view.Text != " pasted text " {
		t.Fatalf("paste text = %q", view.Text)
	}
	view.HandleKeyAt("enter", time.Now().Add(200*time.Millisecond))
	if got, ok := view.LastSubmitted(); !ok || got != "pasted text" {
		t.Fatalf("submitted = %q ok=%v", got, ok)
	}

	view = NewCustomPromptView("Edit goal", "Type", "draft", "")
	view.HandleKeyAt("esc", time.Now())
	if !view.IsComplete() || view.Completion != CustomPromptCancelled {
		t.Fatalf("cancel completion = %s complete=%v", view.Completion, view.IsComplete())
	}
}

func TestNormalizePastedTextMatchesRust(t *testing.T) {
	// Rust #38704: mixed CRLF, bare CR, and LF line endings each become a
	// single LF; CRLF pairs must not double into two line breaks.
	if got := NormalizePastedText("one\r\ntwo\rthree\nfour\r\nfive"); got != "one\ntwo\nthree\nfour\nfive" {
		t.Fatalf("NormalizePastedText = %q", got)
	}
	if got := NormalizePastedText("plain"); got != "plain" {
		t.Fatalf("NormalizePastedText(plain) = %q", got)
	}

	view := NewCustomPromptView("Edit goal", "Type a goal objective and press Enter", "", "")
	if !view.HandlePaste("a\r\nb\rc\nd") || view.Text != "a\nb\nc\nd" {
		t.Fatalf("pasted text = %q", view.Text)
	}
}

func TestCustomPromptVimModeMatchesRustComposerPreferences(t *testing.T) {
	// Rust #39618: custom prompts start in insert mode, first Esc enters
	// normal mode, and Vim keys edit while preserving Esc cancellation.
	view := NewCustomPromptView("Edit goal", "Type a goal objective", "", "")
	view.SetVimEnabled(true)
	if !view.VimEnabled() || !view.VimInsert() {
		t.Fatalf("vim insert start = enabled=%v insert=%v", view.VimEnabled(), view.VimInsert())
	}
	for _, ch := range "hello" {
		view.HandleRuneAt(ch, time.Now())
	}
	view.HandleKeyAt("esc", time.Now())
	if view.VimInsert() {
		t.Fatal("first esc should enter normal mode, not cancel")
	}
	if view.IsComplete() {
		t.Fatal("first esc should not cancel the prompt")
	}
	view.HandleKeyAt("0", time.Now())
	view.HandleKeyAt("x", time.Now())
	if got := view.Text; got != "ello" {
		t.Fatalf("x in normal mode = %q, want ello", got)
	}
	// r replaces the character under the cursor and stays in normal mode.
	view.HandleKeyAt("r", time.Now())
	view.HandleKeyAt("H", time.Now())
	if got := view.Text; got != "Hllo" {
		t.Fatalf("rH = %q, want Hllo (ello with first char replaced)", got)
	}
	if view.VimInsert() {
		t.Fatal("replace_char should stay in normal mode")
	}
	// i enters insert mode; second esc cancels the whole prompt.
	view.HandleKeyAt("i", time.Now())
	if !view.VimInsert() {
		t.Fatal("i should enter insert mode")
	}
	view.HandleKeyAt("esc", time.Now())
	view.HandleKeyAt("esc", time.Now())
	if !view.IsComplete() || view.Completion != CustomPromptCancelled {
		t.Fatalf("esc from normal mode should cancel: completion=%s", view.Completion)
	}

	// Mode-aware footer hints.
	insert := NewCustomPromptView("Goal", "Type", "", "")
	insert.SetVimEnabled(true)
	if rows := insert.Rows(); !bottomPaneContainsRow(rows, "Press enter to confirm or esc to enter normal mode") {
		t.Fatalf("insert hint rows = %#v", rows)
	}
	insert.HandleKeyAt("esc", time.Now())
	if rows := insert.Rows(); !bottomPaneContainsRow(rows, "Vim normal · i to insert · esc to cancel") {
		t.Fatalf("normal hint rows = %#v", rows)
	}
}

func TestCustomPromptEditorMotionsMatchComposer(t *testing.T) {
	view := NewCustomPromptView("Name thread", "Type a name", "one two three", "")
	view.MoveLineStart()
	view.MoveWord(1)
	if view.Cursor() != 4 {
		t.Fatalf("MoveWord cursor = %d, want 4 (start of two)", view.Cursor())
	}
	view.MoveWordEnd()
	if view.Cursor() != 7 {
		t.Fatalf("MoveWordEnd cursor = %d, want 7 (end of two)", view.Cursor())
	}
	view.MoveWord(-1)
	if view.Cursor() != 4 {
		t.Fatalf("MoveWord back cursor = %d, want 4 (start of two)", view.Cursor())
	}
	view.MoveLineEnd()
	if view.Cursor() != len([]rune(view.Text)) {
		t.Fatalf("MoveLineEnd cursor = %d, want %d", view.Cursor(), len([]rune(view.Text)))
	}
	view.DeleteWordBackward()
	if got := view.Text; got != "one two " {
		t.Fatalf("DeleteWordBackward = %q, want 'one two '", got)
	}
}
