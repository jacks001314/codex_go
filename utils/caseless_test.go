package utils

import (
	"testing"
	"unicode/utf16"
)

func utf16Range(text string, r [2]int) (int, int) {
	start := len(utf16.Encode([]rune(text[:r[0]])))
	end := len(utf16.Encode([]rune(text[:r[1]])))
	return start, end
}

// TestFoldedRangesMatchRustDefaultCaseFold mirrors Rust #60759
// (`thread_search_occurrences_reads_paginated_projection`): matches follow
// Unicode default case folding, including final sigma and the "ß" -> "ss"
// expansion, and ranges stay on source character boundaries.
func TestFoldedRangesMatchRustDefaultCaseFold(t *testing.T) {
	text := "😀 ΟΣ ß partial ꟒"
	cases := []struct {
		term         string
		wantStartEnd [2]int
	}{
		{term: "partial", wantStartEnd: [2]int{8, 15}},
		{term: "Σ", wantStartEnd: [2]int{4, 5}},
		{term: "σ", wantStartEnd: [2]int{4, 5}},
		{term: "ς", wantStartEnd: [2]int{4, 5}},
		{term: "ΟΣ", wantStartEnd: [2]int{3, 5}},
		{term: "ss", wantStartEnd: [2]int{6, 7}},
		{term: "ꟓ", wantStartEnd: [2]int{16, 17}},
	}
	for _, tc := range cases {
		got := FoldedRanges(text, tc.term, 4)
		if len(got) != 1 {
			t.Fatalf("term %q matched %d ranges: %#v", tc.term, len(got), got)
		}
		start, end := utf16Range(text, got[0])
		if start != tc.wantStartEnd[0] || end != tc.wantStartEnd[1] {
			t.Fatalf("term %q range = %d..%d, want %d..%d", tc.term, start, end, tc.wantStartEnd[0], tc.wantStartEnd[1])
		}
	}
}
