package filesearch

import (
	"math"
	"sort"
	"strings"
)

type Match struct {
	Indices []int
	Score   int
}

func FuzzyMatch(haystack string, needle string) (Match, bool) {
	if needle == "" {
		return Match{Indices: []int{}, Score: math.MaxInt32}, true
	}
	loweredChars, loweredToOriginal := lowerExpanded(haystack)
	loweredNeedle, _ := lowerExpanded(needle)
	result := make([]int, 0, len(loweredNeedle))
	// Rust #45475: track the first matched position in the lowercased text
	// directly. Reconstructing it from the original index would land on the
	// expansion's first character (for example the 'i' of 'İ' -> "i\u0307"),
	// awarding a wrong prefix bonus or gap penalty for matches that begin
	// inside the expansion.
	firstLowerPos := -1
	lastLowerPos := -1
	cursor := 0
	for _, needleCh := range loweredNeedle {
		found := -1
		for cursor < len(loweredChars) {
			if loweredChars[cursor] == needleCh {
				found = cursor
				cursor++
				break
			}
			cursor++
		}
		if found < 0 {
			return Match{}, false
		}
		result = append(result, loweredToOriginal[found])
		if firstLowerPos < 0 {
			firstLowerPos = found
		}
		lastLowerPos = found
	}
	// Score using the actual lowered positions, even within a character
	// expansion.
	if firstLowerPos < 0 {
		return Match{}, false
	}
	if lastLowerPos < 0 {
		lastLowerPos = firstLowerPos
	}
	window := (lastLowerPos - firstLowerPos + 1) - len(loweredNeedle)
	if window < 0 {
		window = 0
	}
	score := window
	if firstLowerPos == 0 {
		score -= 100
	}
	sort.Ints(result)
	result = dedupeInts(result)
	return Match{Indices: result, Score: score}, true
}

func lowerExpanded(value string) ([]rune, []int) {
	loweredChars := []rune{}
	loweredToOriginal := []int{}
	for originalIndex, ch := range []rune(value) {
		for _, lowered := range lowerRuneExpanded(ch) {
			loweredChars = append(loweredChars, lowered)
			loweredToOriginal = append(loweredToOriginal, originalIndex)
		}
	}
	return loweredChars, loweredToOriginal
}

func lowerRuneExpanded(ch rune) []rune {
	if ch == '\u0130' {
		return []rune{'i', '\u0307'}
	}
	return []rune(strings.ToLower(string(ch)))
}

func dedupeInts(values []int) []int {
	if len(values) < 2 {
		return values
	}
	write := 1
	for read := 1; read < len(values); read++ {
		if values[read] != values[read-1] {
			values[write] = values[read]
			write++
		}
	}
	return values[:write]
}
