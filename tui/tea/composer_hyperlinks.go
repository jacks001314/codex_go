package tea

import (
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"

	codextui "codex_go/tui"
)

const composerMaxHyperlinkDestination = 8 * 1024

type composerHyperlink struct {
	startRune   int
	endRune     int
	destination string
}

// composerHyperlinks locates visible HTTP(S) destinations in the draft using
// composer rune offsets, so each wrapped or scrolled visual fragment can carry
// the complete OSC-8 target (Rust #40720).
func (m *Model) composerHyperlinks() []composerHyperlink {
	if m == nil {
		return nil
	}
	value := m.composer.Value()
	if m.composerLinkValid && m.composerLinkValue == value {
		return m.composerLinkCache
	}
	lines := strings.Split(value, "\n")
	links := []composerHyperlink{}
	globalRune := 0
	for lineIndex, line := range lines {
		for _, link := range codextui.WebLinksInText(line) {
			if len(link.Destination) > composerMaxHyperlinkDestination {
				continue
			}
			start := displayColumnToRuneIndex(line, link.Start)
			end := displayColumnToRuneIndex(line, link.End)
			if end <= start {
				continue
			}
			links = append(links, composerHyperlink{
				startRune:   globalRune + start,
				endRune:     globalRune + end,
				destination: link.Destination,
			})
		}
		globalRune += utf8.RuneCountInString(line)
		if lineIndex < len(lines)-1 {
			globalRune++
		}
	}
	m.composerLinkValue = value
	m.composerLinkCache = links
	m.composerLinkValid = true
	return m.composerLinkCache
}

func (m *Model) annotateComposerWindowHyperlinks(rows []string, scroll int) {
	links := m.composerHyperlinks()
	if len(links) == 0 {
		return
	}
	visualRows := m.composerVisualRows()
	for index := range rows {
		globalRow := scroll + index
		if globalRow < 0 || globalRow >= len(visualRows) {
			continue
		}
		row := visualRows[globalRow]
		for _, link := range links {
			overlapStart := max(link.startRune, row.startRune)
			overlapEnd := min(link.endRune, row.endRune)
			if overlapEnd <= overlapStart {
				continue
			}
			from := m.composerPromptWidth +
				codextui.DisplayWidth(string(row.text[:overlapStart-row.startRune]))
			to := m.composerPromptWidth +
				codextui.DisplayWidth(string(row.text[:overlapEnd-row.startRune]))
			rows[index] = annotateDisplayRange(rows[index], from, to, link.destination)
		}
	}
}

// annotateInlineComposerHyperlinks applies the same wrap-aware mapping to the
// fixed-height inline textarea. Bubbles keeps the caret visible, so the visible
// window is the caret row moved to the bottom when it overflows.
func (m *Model) annotateInlineComposerHyperlinks(content string) string {
	if m == nil || !strings.Contains(m.composer.Value(), "http") {
		return content
	}
	rows := strings.Split(content, "\n")
	target := min(defaultComposerHeight, len(rows))
	if target <= 0 {
		return content
	}
	maxScroll := max(m.composerContentRows()-target, 0)
	scroll := clampModalInt(m.composerCursorVisualRow()-target+1, 0, maxScroll)
	m.annotateComposerWindowHyperlinks(rows, scroll)
	return strings.Join(rows, "\n")
}

func displayColumnToRuneIndex(text string, column int) int {
	if column <= 0 {
		return 0
	}
	used := 0
	runeOffset := 0
	graphemes := uniseg.NewGraphemes(text)
	for graphemes.Next() {
		width := graphemes.Width()
		if column < used+width {
			return runeOffset
		}
		used += width
		runeOffset += utf8.RuneCountInString(graphemes.Str())
		if used >= column {
			return runeOffset
		}
	}
	return runeOffset
}

// annotateDisplayRange wraps one visible display-cell range in an OSC-8 link
// while preserving surrounding ANSI styling and selection-reverse sequences.
func annotateDisplayRange(line string, from int, to int, destination string) string {
	if line == "" || from >= to {
		return line
	}
	var out strings.Builder
	out.Grow(len(line) + len(destination) + 16)
	column := 0
	opened := false
	for index := 0; index < len(line); {
		if line[index] == 0x1b {
			end := sparkleSequenceEnd(line, index)
			out.WriteString(line[index:end])
			index = end
			continue
		}
		graphemes := uniseg.NewGraphemes(line[index:])
		if !graphemes.Next() {
			break
		}
		grapheme := graphemes.Str()
		width := max(graphemes.Width(), 1)
		selected := column < to && column+width > from
		if selected && !opened {
			out.WriteString("\x1b]8;;")
			out.WriteString(destination)
			out.WriteByte(0x07)
			opened = true
		}
		if !selected && opened {
			out.WriteString("\x1b]8;;\x07")
			opened = false
		}
		out.WriteString(grapheme)
		column += width
		index += len(grapheme)
	}
	if opened {
		out.WriteString("\x1b]8;;\x07")
	}
	return out.String()
}
