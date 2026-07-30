package tui

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// Rust parity: codex-rs/tui/src/text_formatting.rs.

func CapitalizeFirst(text string) string {
	if text == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(text)
	if first == utf8.RuneError && size == 0 {
		return ""
	}
	return strings.ToUpper(string(first)) + text[size:]
}

func TruncateText(text string, maxGraphemes int) string {
	if maxGraphemes <= 0 {
		return ""
	}
	overflow, cutAtMax := graphemeBoundaryAfter(text, maxGraphemes)
	if !overflow {
		return text
	}
	if maxGraphemes < 3 {
		return text[:cutAtMax]
	}
	_, cutAtPrefix := graphemeBoundaryAfter(text, maxGraphemes-3)
	return text[:cutAtPrefix] + "..."
}

func graphemeBoundaryAfter(text string, count int) (bool, int) {
	if count <= 0 {
		return text != "", 0
	}
	graphemes := uniseg.NewGraphemes(text)
	seen := 0
	cut := len(text)
	for graphemes.Next() {
		if seen == count {
			start, _ := graphemes.Positions()
			return true, start
		}
		seen++
		_, end := graphemes.Positions()
		if seen == count {
			cut = end
		}
	}
	return false, cut
}

func FormatJSONCompact(text string) (string, bool) {
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return "", false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return addJSONSeparatorSpaces(string(data)), true
}

func FormatAndTruncateToolResult(text string, maxLines int, lineWidth int) string {
	maxChars := maxLines*lineWidth - maxLines
	if maxChars < 0 {
		maxChars = 0
	}
	if formatted, ok := FormatJSONCompact(text); ok {
		return TruncateText(formatted, maxChars)
	}
	return TruncateText(text, maxChars)
}

func CenterTruncatePath(path string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if DisplayWidth(path) <= maxWidth {
		return path
	}
	separator := string(filepath.Separator)
	hasLeadingSep := strings.HasPrefix(path, separator)
	hasTrailingSep := strings.HasSuffix(path, separator)
	rawSegments := strings.Split(path, separator)
	if hasLeadingSep && len(rawSegments) > 0 && rawSegments[0] == "" {
		rawSegments = rawSegments[1:]
	}
	if hasTrailingSep && len(rawSegments) > 0 && rawSegments[len(rawSegments)-1] == "" {
		rawSegments = rawSegments[:len(rawSegments)-1]
	}
	if len(rawSegments) == 0 {
		if hasLeadingSep && DisplayWidth(separator) <= maxWidth {
			return separator
		}
		return "…"
	}

	type pathSegment struct {
		original    string
		text        string
		truncatable bool
		isSuffix    bool
	}

	assemble := func(leading bool, segments []pathSegment) string {
		var builder strings.Builder
		if leading {
			builder.WriteString(separator)
		}
		for _, segment := range segments {
			current := builder.String()
			if current != "" && !strings.HasSuffix(current, separator) {
				builder.WriteString(separator)
			}
			builder.WriteString(segment.text)
		}
		return builder.String()
	}

	segmentCount := len(rawSegments)
	combos := make([][2]int, 0)
	for left := 1; left <= segmentCount; left++ {
		minRight := 1
		if left == segmentCount {
			minRight = 0
		}
		for right := minRight; right <= segmentCount-left; right++ {
			combos = append(combos, [2]int{left, right})
		}
	}
	desiredSuffix := 0
	if segmentCount > 1 {
		desiredSuffix = min(2, segmentCount-1)
	}
	prioritized := make([][2]int, 0, len(combos))
	fallback := make([][2]int, 0, len(combos))
	for _, combo := range combos {
		if combo[1] >= desiredSuffix {
			prioritized = append(prioritized, combo)
		} else {
			fallback = append(fallback, combo)
		}
	}
	sortCombos := func(items [][2]int) {
		sort.Slice(items, func(i, j int) bool {
			leftA, rightA := items[i][0], items[i][1]
			leftB, rightB := items[j][0], items[j][1]
			if leftA != leftB {
				return leftA > leftB
			}
			if rightA != rightB {
				return rightA > rightB
			}
			return leftA+rightA > leftB+rightB
		})
	}
	sortCombos(prioritized)
	sortCombos(fallback)

	fitSegments := func(segments []pathSegment, allowFrontTruncate bool) (string, bool) {
		for {
			candidate := assemble(hasLeadingSep, segments)
			width := DisplayWidth(candidate)
			if width <= maxWidth {
				return candidate, true
			}
			if !allowFrontTruncate {
				return "", false
			}
			indices := make([]int, 0, len(segments))
			for idx := len(segments) - 1; idx >= 0; idx-- {
				if segments[idx].truncatable && segments[idx].isSuffix {
					indices = append(indices, idx)
				}
			}
			for idx := len(segments) - 1; idx >= 0; idx-- {
				if segments[idx].truncatable && !segments[idx].isSuffix {
					indices = append(indices, idx)
				}
			}
			if len(indices) == 0 {
				return "", false
			}
			changed := false
			for _, idx := range indices {
				originalWidth := DisplayWidth(segments[idx].original)
				if originalWidth <= maxWidth && segmentCount > 2 {
					continue
				}
				segmentWidth := DisplayWidth(segments[idx].text)
				otherWidth := width - segmentWidth
				allowedWidth := max(maxWidth-otherWidth, 1)
				newText := frontTruncate(segments[idx].original, allowedWidth)
				if newText != segments[idx].text {
					segments[idx].text = newText
					changed = true
					break
				}
			}
			if !changed {
				return "", false
			}
		}
	}

	for _, combo := range append(prioritized, fallback...) {
		leftCount, rightCount := combo[0], combo[1]
		segments := make([]pathSegment, 0, leftCount+rightCount+1)
		for _, segment := range rawSegments[:leftCount] {
			segments = append(segments, pathSegment{original: segment, text: segment, truncatable: true})
		}
		needEllipsis := leftCount+rightCount < segmentCount
		if needEllipsis {
			segments = append(segments, pathSegment{original: "…", text: "…"})
		}
		if rightCount > 0 {
			for _, segment := range rawSegments[segmentCount-rightCount:] {
				segments = append(segments, pathSegment{original: segment, text: segment, truncatable: true, isSuffix: true})
			}
		}
		allowFrontTruncate := needEllipsis || segmentCount <= 2
		if candidate, ok := fitSegments(segments, allowFrontTruncate); ok {
			return candidate
		}
	}
	return frontTruncate(path, maxWidth)
}

func ProperJoin(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

func addJSONSeparatorSpaces(text string) string {
	var out strings.Builder
	inString := false
	escaped := false
	for _, r := range text {
		out.WriteRune(r)
		if escaped {
			escaped = false
			continue
		}
		if inString && r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if !inString && (r == ':' || r == ',') {
			out.WriteRune(' ')
		}
	}
	return strings.ReplaceAll(out.String(), ", }", "}")
}

func frontTruncate(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if DisplayWidth(text) <= maxWidth {
		return text
	}
	if maxWidth == 1 {
		return "…"
	}
	clusters := make([]string, 0, uniseg.GraphemeClusterCount(text))
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		clusters = append(clusters, graphemes.Str())
	}
	kept := make([]string, 0, len(clusters))
	used := 1
	for i := len(clusters) - 1; i >= 0; i-- {
		width := DisplayWidth(clusters[i])
		if used+width > maxWidth {
			break
		}
		kept = append(kept, clusters[i])
		used += width
	}
	var out strings.Builder
	out.WriteRune('\u2026')
	for i := len(kept) - 1; i >= 0; i-- {
		out.WriteString(kept[i])
	}
	return out.String()
}
