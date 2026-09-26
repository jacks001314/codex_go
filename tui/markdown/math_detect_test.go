package markdown

import "testing"

// Mirrors Rust's inline admission rules, including #48551's `$0$` allowance and
// the prose cases pinned by `unicode_math_preserves_markdown_contexts`.
func TestInlineMathAdmittedLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		formula  string
		next     rune
		hasNext  bool
		admitted bool
	}{
		{name: "zero is math (#48551)", formula: "0", next: '.', hasNext: true, admitted: true},
		{name: "bare number is money", formula: "5", next: ' ', hasNext: true, admitted: false},
		{name: "bare number at end", formula: "10", admitted: false},
		{name: "initialism", formula: "USD", next: '+', hasNext: true, admitted: false},
		{name: "single uppercase letter", formula: "A", admitted: true},
		{name: "adjacent alphanumeric", formula: "x", next: 'y', hasNext: true, admitted: false},
		{name: "adjacent unicode alphanumeric", formula: "x", next: '\u65e5', hasNext: true, admitted: false},
		{name: "surrounded by prose", formula: "x", next: ' ', hasNext: true, admitted: true},
		{name: "leading digit with operators", formula: "1+1", admitted: true},
		{name: "trailing whitespace", formula: "\\alpha ", admitted: false},
	}
	for _, testCase := range cases {
		if got := inlineMathAdmitted(testCase.formula, testCase.next, testCase.hasNext); got != testCase.admitted {
			t.Errorf("%s: inlineMathAdmitted(%q, %q, %v) = %v, want %v",
				testCase.name, testCase.formula, testCase.next, testCase.hasNext, got, testCase.admitted)
		}
	}
}

// Mirrors Rust's `escaped`: an odd run of backslashes escapes the delimiter.
func TestMathEscapedLikeRust(t *testing.T) {
	cases := []struct {
		input  string
		offset int
		want   bool
	}{
		{input: `$`, offset: 0, want: false},
		{input: `\$`, offset: 1, want: true},
		{input: `\\$`, offset: 2, want: false},
		{input: `\\\$`, offset: 3, want: true},
	}
	for _, testCase := range cases {
		if got := mathEscaped(testCase.input, testCase.offset); got != testCase.want {
			t.Errorf("mathEscaped(%q, %d) = %v, want %v", testCase.input, testCase.offset, got, testCase.want)
		}
	}
}

// Mirrors Rust's opener table: `$$`/`\[` are display, `\(`/`$` are inline.
func TestMathOpenDelimiterLikeRust(t *testing.T) {
	cases := []struct {
		rest    string
		open    string
		close   string
		display bool
		ok      bool
	}{
		{rest: "$$x$$", open: "$$", close: "$$", display: true, ok: true},
		{rest: `\[x\]`, open: `\[`, close: `\]`, display: true, ok: true},
		{rest: `\(x\)`, open: `\(`, close: `\)`, display: false, ok: true},
		{rest: "$x$", open: "$", close: "$", display: false, ok: true},
		{rest: "x", ok: false},
	}
	for _, testCase := range cases {
		open, close, display, ok := mathOpenDelimiter(testCase.rest)
		if open != testCase.open || close != testCase.close || display != testCase.display || ok != testCase.ok {
			t.Errorf("mathOpenDelimiter(%q) = (%q, %q, %v, %v), want (%q, %q, %v, %v)",
				testCase.rest, open, close, display, ok, testCase.open, testCase.close, testCase.display, testCase.ok)
		}
	}
}
