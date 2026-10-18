package markdown

import (
	"strings"

	"codex_go/utils"
)

// renderedListKind distinguishes sibling groups that goldmark would parse as
// separate lists. Rust #46899 applies the uniform policy per list.
type renderedListKind uint8

const (
	renderedListNone renderedListKind = iota
	renderedListBullet
	renderedListOrdered
	renderedListTask
)

type renderedListLine struct {
	quoteDepth int
	indent     int
	kind       renderedListKind
}

type renderedListGroupKey struct {
	quoteDepth int
	indent     int
	kind       renderedListKind
}

// applyUniformListSpacing implements Rust's completed-transcript list policy:
// when any sibling in a list spans multiple rendered rows, separate every
// sibling with one blank line. Single-line lists stay compact.
func applyUniformListSpacing(rendered string) string {
	if !strings.Contains(rendered, "\n") {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	infos := make([]renderedListLine, len(lines))
	for index, line := range lines {
		infos[index] = parseRenderedListLine(line)
	}

	markers := make([]int, 0, len(lines))
	byKey := map[renderedListGroupKey][]int{}
	for index, info := range infos {
		if info.kind == renderedListNone {
			continue
		}
		key := renderedListGroupKey{quoteDepth: info.quoteDepth, indent: info.indent, kind: info.kind}
		markers = append(markers, index)
		byKey[key] = append(byKey[key], index)
	}
	if len(markers) == 0 {
		return rendered
	}

	end := make(map[int]int, len(markers))
	multiline := make(map[int]bool, len(markers))
	for _, marker := range markers {
		itemEnd, itemMultiline := renderedListItemEnd(lines, infos, marker)
		end[marker] = itemEnd
		multiline[marker] = itemMultiline
	}

	insert := map[int]string{}
	for _, indices := range byKey {
		groupStart := 0
		for groupStart < len(indices) {
			groupEnd := groupStart + 1
			for groupEnd < len(indices) && end[indices[groupEnd-1]] == indices[groupEnd] {
				groupEnd++
			}
			group := indices[groupStart:groupEnd]
			uniform := false
			for _, marker := range group {
				if multiline[marker] {
					uniform = true
					break
				}
			}
			if uniform {
				for _, marker := range group[1:] {
					if marker > 0 && !renderedLineBlank(lines[marker-1]) {
						insert[marker] = renderedQuoteBlank(infos[marker].quoteDepth)
					}
				}
			}
			groupStart = groupEnd
		}
	}
	if len(insert) == 0 {
		return rendered
	}
	out := make([]string, 0, len(lines)+len(insert))
	for index, line := range lines {
		if blank, ok := insert[index]; ok {
			out = append(out, blank)
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func parseRenderedListLine(line string) renderedListLine {
	text := utils.StripANSI(line)
	quoteDepth, text := splitRenderedQuotePrefix(text)
	indent := len(text) - len(strings.TrimLeft(text, " "))
	body := text[indent:]
	switch {
	case strings.HasPrefix(body, "• "), body == "•",
		strings.HasPrefix(body, "- "), strings.HasPrefix(body, "* "), strings.HasPrefix(body, "+ "):
		return renderedListLine{quoteDepth: quoteDepth, indent: indent, kind: renderedListBullet}
	case strings.HasPrefix(body, "☑ "), strings.HasPrefix(body, "☐ "):
		return renderedListLine{quoteDepth: quoteDepth, indent: indent, kind: renderedListTask}
	}
	digits := 0
	for digits < len(body) && body[digits] >= '0' && body[digits] <= '9' {
		digits++
	}
	if digits > 0 && strings.HasPrefix(body[digits:], ". ") {
		return renderedListLine{quoteDepth: quoteDepth, indent: indent, kind: renderedListOrdered}
	}
	return renderedListLine{quoteDepth: quoteDepth, indent: indent}
}

func renderedListItemEnd(lines []string, infos []renderedListLine, start int) (int, bool) {
	key := infos[start]
	multiline := false
	sawBlank := false
	for index := start + 1; index < len(infos); index++ {
		info := infos[index]
		if renderedLineBlank(lines[index]) {
			sawBlank = true
			continue
		}
		if info.kind != renderedListNone {
			if info.quoteDepth == key.quoteDepth && info.indent == key.indent && info.kind == key.kind {
				return index, multiline
			}
			if info.quoteDepth < key.quoteDepth || info.indent < key.indent {
				return index, multiline
			}
			if info.quoteDepth == key.quoteDepth && info.indent == key.indent {
				// A different list kind at the same depth starts a new list.
				return index, multiline
			}
			multiline = true
			sawBlank = false
			continue
		}
		if sawBlank && info.quoteDepth == key.quoteDepth && info.indent <= key.indent {
			return index, multiline
		}
		if info.quoteDepth < key.quoteDepth {
			return index, multiline
		}
		multiline = true
		sawBlank = false
	}
	return len(infos), multiline
}

func splitRenderedQuotePrefix(text string) (int, string) {
	depth := 0
	for {
		switch {
		case strings.HasPrefix(text, blockQuoteIndentToken):
			depth++
			text = text[len(blockQuoteIndentToken):]
		case strings.HasPrefix(text, ">"):
			depth++
			text = text[1:]
			if strings.HasPrefix(text, " ") {
				text = text[1:]
			}
		default:
			return depth, text
		}
	}
}

func renderedLineBlank(line string) bool {
	text := strings.TrimSpace(utils.StripANSI(line))
	if text == "" {
		return true
	}
	_, text = splitRenderedQuotePrefix(text)
	return strings.TrimSpace(text) == ""
}

func renderedQuoteBlank(depth int) string {
	if depth <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat(blockQuoteIndentToken, depth), " ")
}
