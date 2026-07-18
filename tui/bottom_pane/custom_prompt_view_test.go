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
