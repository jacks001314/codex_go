package tea

import (
	"math/rand"
	"strings"
	"testing"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

// composerWrappedRowsReference is the previous implementation, kept as the
// oracle for the equivalence test. The optimised version must produce exactly
// the same rows: selection mapping depends on these rune ranges.
func composerWrappedRowsReference(runes []rune, width int) [][]rune {
	if width <= 0 {
		width = 1
	}
	var (
		lines  = [][]rune{{}}
		word   = []rune{}
		row    int
		spaces int
	)
	for _, r := range runes {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			word = append(word, r)
		}
		if spaces > 0 {
			if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces > width {
				row++
				lines = append(lines, []rune{})
			}
			lines[row] = append(lines[row], word...)
			lines[row] = append(lines[row], composerRepeatSpaces(spaces)...)
			spaces = 0
			word = nil
			continue
		}
		if len(word) == 0 {
			continue
		}
		lastWidth := runewidth.RuneWidth(word[len(word)-1])
		if uniseg.StringWidth(string(word))+lastWidth > width {
			if len(lines[row]) > 0 {
				row++
				lines = append(lines, []rune{})
			}
			lines[row] = append(lines[row], word...)
			word = nil
		}
	}
	if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces >= width {
		lines = append(lines, []rune{})
		row++
	}
	lines[row] = append(lines[row], word...)
	lines[row] = append(lines[row], composerRepeatSpaces(spaces+1)...)
	return lines
}

// TestComposerWrappedRowsMatchesReference is the characterisation test for the
// incremental wrapper: every corpus entry must produce identical rows.
func TestComposerWrappedRowsMatchesReference(t *testing.T) {
	corpus := []string{
		"",
		"hello",
		"hello world",
		"hello   world   ",
		strings100("x", 300),
		"a " + strings100("b", 250) + " c",
		"日本語のテキストを折り返します",
		"emoji 👨‍👩‍👧 and flags 🇯🇵🇺🇸 mixed with ascii",
		"combining e\u0301e\u0301e\u0301 sequence",
		"tabs\tand\tnewlines\nhere",
		strings100("word ", 80),
		strings100("超", 120),
		"trailing spaces    ",
		"   leading spaces",
		"a",
	}
	widths := []int{1, 2, 5, 12, 40, 80, 120}
	for _, text := range corpus {
		for _, width := range widths {
			want := composerWrappedRowsReference([]rune(text), width)
			got := composerWrappedRows([]rune(text), width)
			if len(got) != len(want) {
				t.Fatalf("rows for %q at width %d = %d, want %d", text, width, len(got), len(want))
			}
			for index := range want {
				if string(got[index]) != string(want[index]) {
					t.Fatalf("row %d for %q at width %d = %q, want %q", index, text, width, string(got[index]), string(want[index]))
				}
			}
		}
	}
}

// TestComposerWrappedRowsMatchesReferenceRandomised widens the corpus with a
// deterministic mix of ASCII, wide and emoji runes.
func TestComposerWrappedRowsMatchesReferenceRandomised(t *testing.T) {
	randSource := rand.New(rand.NewSource(20261029))
	pieces := []string{"a", "bb", "ccc", "word", " ", "  ", "\n", "\t", "超", "日本語", "👨‍👩‍👧", "🇯🇵", "e\u0301", "—"}
	for iteration := 0; iteration < 200; iteration++ {
		var builder strings.Builder
		length := randSource.Intn(160)
		for index := 0; index < length; index++ {
			builder.WriteString(pieces[randSource.Intn(len(pieces))])
		}
		text := builder.String()
		width := 1 + randSource.Intn(40)
		want := composerWrappedRowsReference([]rune(text), width)
		got := composerWrappedRows([]rune(text), width)
		if len(got) != len(want) {
			t.Fatalf("iteration %d: rows for %q at width %d = %d, want %d", iteration, text, width, len(got), len(want))
		}
		for index := range want {
			if string(got[index]) != string(want[index]) {
				t.Fatalf("iteration %d: row %d for %q at width %d = %q, want %q", iteration, index, text, width, string(got[index]), string(want[index]))
			}
		}
	}
}

func strings100(unit string, count int) string {
	var builder strings.Builder
	for i := 0; i < count; i++ {
		builder.WriteString(unit)
	}
	return builder.String()
}
