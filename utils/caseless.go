package utils

import (
	"strings"
	"unicode"

	"golang.org/x/text/cases"
)

// caseFolding mirrors Rust's thread-store LiteralMatcher (#60759): lowercase
// the character first, then apply Unicode default case folding. Lowercasing
// covers characters newer than the folding tables, folding covers expansions
// like "ß" -> "ss" and the final sigma forms.
var caseFolding = cases.Fold()

// supplementalLowercase covers case pairs added after the Unicode data
// compiled into the standard library and golang.org/x/text. Rust's older
// releases already lowercase them, so the matcher keeps the same behaviour
// instead of silently missing those letters.
var supplementalLowercase = map[rune]rune{
	0x1C89: 0x1C8A, // Unicode 16: LATIN CAPITAL/SMALL LETTER TJE
	0xA7D2: 0xA7D3, // Unicode 17: LATIN CAPITAL/SMALL LETTER ...
}

func lowerRune(r rune) rune {
	lowered := unicode.ToLower(r)
	if lowered == r {
		if mapped, ok := supplementalLowercase[r]; ok {
			return mapped
		}
	}
	return lowered
}

func foldRune(r rune) string {
	return caseFolding.String(string(lowerRune(r)))
}

type foldBoundary struct {
	// foldEnd is the byte offset in the folded text just past this source rune.
	foldEnd int
	origEnd int
	origRaw int
}

// FoldedRanges returns the byte ranges in text that match needle under Unicode
// case folding, capped at limit. The ranges refer to the original text, so a
// match that spans a folded expansion ("ss" matching "ß") still reports the
// whole source character.
func FoldedRanges(text string, needle string, limit int) [][2]int {
	if text == "" || needle == "" {
		return nil
	}
	foldedNeedle := foldString(needle)
	if foldedNeedle == "" {
		return nil
	}
	folded, boundaries := foldWithBoundaries(text)
	capacity := limit
	if capacity <= 0 {
		capacity = 8
	}
	ranges := make([][2]int, 0, capacity)
	cursor := 0
	searchFrom := 0
	for limit <= 0 || len(ranges) < limit {
		relative := strings.Index(folded[searchFrom:], foldedNeedle)
		if relative < 0 {
			break
		}
		start := searchFrom + relative
		end := start + len(foldedNeedle)
		for cursor < len(boundaries) && boundaries[cursor].foldEnd <= start {
			cursor++
		}
		if cursor >= len(boundaries) {
			break
		}
		origStart := boundaries[cursor].origRaw
		for cursor < len(boundaries) && boundaries[cursor].foldEnd < end {
			cursor++
		}
		if cursor >= len(boundaries) {
			break
		}
		ranges = append(ranges, [2]int{origStart, boundaries[cursor].origEnd})
		searchFrom = end
	}
	return ranges
}

func foldString(value string) string {
	var builder strings.Builder
	for _, r := range value {
		builder.WriteString(foldRune(r))
	}
	return builder.String()
}

func foldWithBoundaries(text string) (string, []foldBoundary) {
	var builder strings.Builder
	boundaries := make([]foldBoundary, 0, len(text))
	origOffset := 0
	for _, r := range text {
		chunk := foldRune(r)
		builder.WriteString(chunk)
		origEnd := origOffset + len(string(r))
		boundaries = append(boundaries, foldBoundary{foldEnd: builder.Len(), origEnd: origEnd, origRaw: origOffset})
		origOffset = origEnd
	}
	return builder.String(), boundaries
}
