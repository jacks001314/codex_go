package textarea

import "testing"

func TestVimWordTextObjectsMatchRustCore(t *testing.T) {
	area := NewTextArea("alpha.beta gamma")
	area.SetCursor(len("alpha"))
	if got, ok := area.TextObjectRange(VimTextObjectWord, VimTextObjectInner); !ok || got != (TextRange{Start: len("alpha"), End: len("alpha.")}) {
		t.Fatalf("inner separator word = %#v ok=%v", got, ok)
	}
	area.SetCursor(len("alpha.be"))
	if got, ok := area.TextObjectRange(VimTextObjectWord, VimTextObjectInner); !ok || got != (TextRange{Start: len("alpha."), End: len("alpha.beta")}) {
		t.Fatalf("inner word = %#v ok=%v", got, ok)
	}
	if got, ok := area.TextObjectRange(VimTextObjectBigWord, VimTextObjectInner); !ok || got != (TextRange{Start: 0, End: len("alpha.beta")}) {
		t.Fatalf("big word = %#v ok=%v", got, ok)
	}
	if got, ok := area.TextObjectRange(VimTextObjectBigWord, VimTextObjectAround); !ok || got != (TextRange{Start: 0, End: len("alpha.beta ")}) {
		t.Fatalf("around big word = %#v ok=%v", got, ok)
	}
	area.SetCursor(len("alpha.beta gamma"))
	if got, ok := area.TextObjectRange(VimTextObjectWord, VimTextObjectAround); !ok || got != (TextRange{Start: len("alpha.beta"), End: len("alpha.beta gamma")}) {
		t.Fatalf("around last word = %#v ok=%v", got, ok)
	}
}

func TestVimPairedAndQuotedTextObjectsMatchRustCore(t *testing.T) {
	area := NewTextArea("call(one [two] {three})")
	area.SetCursor(len("call(one [tw"))
	if got, ok := area.TextObjectRange(VimTextObjectBrackets, VimTextObjectInner); !ok || got != (TextRange{Start: len("call(one ["), End: len("call(one [two")}) {
		t.Fatalf("inner brackets = %#v ok=%v", got, ok)
	}
	if got, ok := area.TextObjectRange(VimTextObjectParentheses, VimTextObjectAround); !ok || got != (TextRange{Start: len("call"), End: len("call(one [two] {three})")}) {
		t.Fatalf("around parentheses = %#v ok=%v", got, ok)
	}

	quoted := NewTextArea("say \"hello \\\"ignored\\\"\" and `code`")
	quoted.SetCursor(len("say \"hel"))
	if got, ok := quoted.TextObjectRange(VimTextObjectDoubleQuote, VimTextObjectInner); !ok || got != (TextRange{Start: len("say \""), End: len("say \"hello \\\"ignored\\\"")}) {
		t.Fatalf("inner quote = %#v ok=%v", got, ok)
	}
	if got, ok := quoted.TextObjectRange(VimTextObjectDoubleQuote, VimTextObjectAround); !ok || got != (TextRange{Start: len("say "), End: len("say \"hello \\\"ignored\\\"\"")}) {
		t.Fatalf("around quote = %#v ok=%v", got, ok)
	}
	quoted.SetCursor(len("say \"hello \\\"ignored\\\"\" and `co"))
	if got, ok := quoted.TextObjectRange(VimTextObjectBacktick, VimTextObjectInner); !ok || got != (TextRange{Start: len("say \"hello \\\"ignored\\\"\" and `"), End: len("say \"hello \\\"ignored\\\"\" and `code")}) {
		t.Fatalf("inner backtick = %#v ok=%v", got, ok)
	}
}

func TestVimTextObjectsSkipElementsAndClampCursor(t *testing.T) {
	area := NewTextArea("before (skip) after (take)")
	area.AddElement(len("before "), len("before (skip)"))
	area.SetCursor(len("before (skip) after (ta"))
	if got, ok := area.TextObjectRange(VimTextObjectParentheses, VimTextObjectInner); !ok || got != (TextRange{Start: len("before (skip) after ("), End: len("before (skip) after (take")}) {
		t.Fatalf("inner parentheses skipping element = %#v ok=%v", got, ok)
	}

	unicode := NewTextArea("你好吗 word")
	unicode.SetCursor(2)
	if unicode.Cursor != 0 {
		t.Fatalf("cursor should clamp to rune boundary, got %d", unicode.Cursor)
	}
	unicode.SetCursor(len("你好吗 "))
	if got, ok := unicode.TextObjectRange(VimTextObjectWord, VimTextObjectInner); !ok || got != (TextRange{Start: len("你好吗 "), End: len("你好吗 word")}) {
		t.Fatalf("unicode word = %#v ok=%v", got, ok)
	}
}
