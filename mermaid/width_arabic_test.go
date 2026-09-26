package mermaid

import "testing"

// TestTextWidthArabicLamAlef mirrors `unicode-width` 0.2.1's Arabic rule: a
// Lam-joining-group character followed, after any run of transparent zero-width
// characters, by an Alef-joining-group character collapses to width 1. Expected
// values were produced by the crate's `UnicodeWidthStr::width`.
func TestTextWidthArabicLamAlef(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"lam alef", "\u0644\u0627", 1},
		{"lam alef madda", "\u0644\u0622", 1},
		{"lam alef hamza above", "\u0644\u0623", 1},
		{"lam alef hamza below", "\u0644\u0625", 1},
		{"lam transparent mark alef", "\u0644\u0651\u0627", 1},
		{"lam transparent mark alef two", "\u0644\u065F\u0627", 1},
		{"lam fatha alef", "\u0644\u064E\u0627", 1},
		{"lam alef fatha", "\u0644\u0627\u064E", 1},
		{"lam with v", "\u06B5\u0627", 1},
		{"lam with v below", "\u06B8\u0627", 1},
		{"lam with dot", "\u076A\u0627", 1},
		{"lam with jeem", "\u08A6\u0627", 1},
		{"lam with khah", "\u08C7\u0627", 1},
		{"lam alef alef", "\u0644\u0627\u0627", 2},
		{"lam lam alef", "\u0644\u0644\u0627", 2},
		{"lam alef lam alef", "\u0644\u0627\u0644\u0627", 2},
		{"zwnj is not transparent", "\u0644\u200C\u0627", 2},
		{"lam alone", "\u0644", 1},
		{"alef alone", "\u0627", 1},
		{"alef madda alone", "\u0622", 1},
		{"alef last alone", "\u0882", 1},
		{"ascii", "ab", 2},
		{"cjk", "\u4F60\u597D", 4},
	}
	for _, testCase := range cases {
		if got := textWidth(testCase.text); got != testCase.want {
			t.Errorf("%s: textWidth(%q) = %d, want %d", testCase.name, testCase.text, got, testCase.want)
		}
	}
}
