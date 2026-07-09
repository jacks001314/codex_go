package requestuserinput

import (
	"strings"
	"unicode"

	codextui "codex_go/internal/tui"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/request_user_input/render.rs.

const MinOverlayHeight = 8

type FooterTip struct {
	Text      string
	Highlight bool
}

type RenderInput struct {
	Sections      LayoutSections
	Progress      string
	QuestionLines []string
	OptionRows    []string
	Notes         string
	FooterTips    []FooterTip
	EmptyOptions  string
}

func RenderQuestion(question string) []string {
	return RenderQuestionWrapped(question, 0)
}

func RenderQuestionWrapped(question string, width int) []string {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil
	}
	if width <= 0 {
		return []string{question}
	}
	return WrapText(question, width)
}

func DesiredHeight(questionLines int, optionsPreferredHeight int, notesVisible bool, notesHeight int, footerHeight int, hasOptions bool) int {
	spacerRows := 0
	if hasOptions {
		if notesVisible {
			spacerRows = 1
		} else {
			spacerRows = DesiredSpacersBetweenSections
		}
	}
	height := questionLines + optionsPreferredHeight + spacerRows + notesHeight + footerHeight + 1 + 2
	if height < MinOverlayHeight {
		return MinOverlayHeight
	}
	return height
}

func RenderUI(input RenderInput, width int) []string {
	height := maxY(input.Sections)
	canvas := make([]string, height)
	put := func(area Rect, lines []string) {
		for idx, line := range lines {
			if idx >= area.Height {
				break
			}
			y := area.Y + idx
			if y < 0 || y >= len(canvas) {
				continue
			}
			canvas[y] = truncateLineWordBoundaryWithEllipsis(line, area.Width)
		}
	}
	if input.Progress != "" && input.Sections.ProgressArea.Height > 0 {
		put(input.Sections.ProgressArea, []string{input.Progress})
	}
	questionLines := input.QuestionLines
	if len(questionLines) == 0 {
		questionLines = input.Sections.QuestionLines
	}
	put(input.Sections.QuestionArea, questionLines)
	if input.Sections.OptionsArea.Height > 0 {
		empty := input.EmptyOptions
		if empty == "" {
			empty = "No options"
		}
		put(input.Sections.OptionsArea, RenderRowsBottomAligned(input.OptionRows, input.Sections.OptionsArea.Height, empty))
	}
	if input.Notes != "" && input.Sections.NotesArea.Height > 0 {
		put(input.Sections.NotesArea, WrapText(input.Notes, input.Sections.NotesArea.Width))
	}
	if len(input.FooterTips) > 0 && input.Sections.FooterLines > 0 {
		footerLines := WrapFooterTips(width, input.FooterTips)
		footerArea := Rect{
			X:      0,
			Y:      input.Sections.NotesArea.Y + input.Sections.NotesArea.Height,
			Width:  width,
			Height: input.Sections.FooterLines,
		}
		put(footerArea, footerLines)
	}
	return canvas
}

func RenderRowsBottomAligned(rows []string, height int, emptyMessage string) []string {
	if height <= 0 {
		return nil
	}
	if len(rows) == 0 {
		rows = []string{emptyMessage}
	}
	if len(rows) > height {
		rows = rows[len(rows)-height:]
	}
	out := make([]string, height)
	offset := height - len(rows)
	for idx, row := range rows {
		out[offset+idx] = row
	}
	return out
}

func WrapFooterTips(width int, tips []FooterTip) []string {
	parts := make([]string, 0, len(tips))
	for _, tip := range tips {
		text := tip.Text
		if tip.Highlight {
			text = "[" + text + "]"
		}
		parts = append(parts, text)
	}
	return wrapJoinedParts(parts, " | ", width)
}

func WrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	lines := []string{}
	current := ""
	for _, word := range words {
		if current == "" {
			current = word
			continue
		}
		if len([]rune(current))+1+len([]rune(word)) <= width {
			current += " " + word
			continue
		}
		lines = append(lines, breakLongWord(current, width)...)
		current = word
	}
	if current != "" {
		lines = append(lines, breakLongWord(current, width)...)
	}
	return lines
}

func wrapJoinedParts(parts []string, separator string, width int) []string {
	if width <= 0 {
		return []string{strings.Join(parts, separator)}
	}
	lines := []string{}
	current := ""
	for _, part := range parts {
		candidate := part
		if current != "" {
			candidate = current + separator + part
		}
		if len([]rune(candidate)) <= width {
			current = candidate
			continue
		}
		if current != "" {
			lines = append(lines, current)
			current = part
			continue
		}
		lines = append(lines, breakLongWord(part, width)...)
		current = ""
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func truncateLineWordBoundaryWithEllipsis(line string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if codextui.DisplayWidth(line) <= maxWidth {
		return line
	}
	const ellipsis = "\u2026"
	ellipsisWidth := codextui.DisplayWidth(ellipsis)
	if ellipsisWidth >= maxWidth {
		return ellipsis
	}
	limit := maxWidth - ellipsisWidth
	used := 0
	lastFitByteEnd := -1
	lastWordBreakByteEnd := -1
	overflowed := false
	for byteIdx, r := range line {
		runeWidth := codextui.DisplayWidth(string(r))
		if used+runeWidth > limit {
			overflowed = true
			break
		}
		used += runeWidth
		byteEnd := byteIdx + len(string(r))
		lastFitByteEnd = byteEnd
		if unicode.IsSpace(r) {
			lastWordBreakByteEnd = byteEnd
		}
	}
	if !overflowed {
		return line
	}
	chosenBreak := lastFitByteEnd
	if lastWordBreakByteEnd >= 0 {
		chosenBreak = lastWordBreakByteEnd
	}
	if chosenBreak < 0 {
		return ellipsis
	}
	out := strings.TrimRightFunc(line[:chosenBreak], unicode.IsSpace)
	if out == "" {
		return ellipsis
	}
	return out + ellipsis
}

func breakLongWord(value string, width int) []string {
	if width <= 0 || len([]rune(value)) <= width {
		return []string{value}
	}
	runes := []rune(value)
	out := []string{}
	for len(runes) > width {
		out = append(out, string(runes[:width]))
		runes = runes[width:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

func maxY(sections LayoutSections) int {
	max := 0
	areas := []Rect{sections.ProgressArea, sections.QuestionArea, sections.OptionsArea, sections.NotesArea}
	for _, area := range areas {
		if y := area.Y + area.Height; y > max {
			max = y
		}
	}
	if footerY := sections.NotesArea.Y + sections.NotesArea.Height + sections.FooterLines; footerY > max {
		max = footerY
	}
	return max
}
