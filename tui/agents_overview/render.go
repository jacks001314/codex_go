package agentsoverview

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Render produces the plain-text dashboard lines for the given terminal
// dimensions, mirroring Rust AgentsOverviewView::render.
func (v *View) Render(termWidth, termHeight int) []string {
	return v.renderLines(termWidth, termHeight, false)
}

// RenderStyled is Render with the Rust ANSI styling applied (bold headers,
// dim meta text, colored status dots, cyan markers/labels).
func (v *View) RenderStyled(termWidth, termHeight int) []string {
	return v.renderLines(termWidth, termHeight, true)
}

func (v *View) renderLines(termWidth, termHeight int, styled bool) []string {
	if v == nil || termWidth < 12 || termHeight < 8 {
		return nil
	}
	inset := strings.Repeat(" ", 2)
	maxWidth := termWidth - 2
	if maxWidth < 0 {
		maxWidth = 0
	}

	lines := make([]string, 0, termHeight)
	// header
	lines = append(lines, renderLine(inset, []span{{text: "Agent command center", style: spanBold}}, maxWidth, styled))
	// summary
	needsYou, working, ready := v.Counts()
	lines = append(lines, renderLine(inset, []span{{text: formatSummary(needsYou, working, ready), style: spanDim}}, maxWidth, styled))
	// divider
	dividerWidth := termWidth - 4
	if dividerWidth < 0 {
		dividerWidth = 0
	}
	lines = append(lines, renderLine(inset, []span{{text: strings.Repeat("─", dividerWidth), style: spanDim}}, maxWidth, styled))

	bodyHeight := termHeight - 5 // header + summary + divider + prompt + footer
	if bodyHeight < 3 {
		bodyHeight = 3
	}
	bodyWidth := termWidth - 4
	if bodyWidth < 0 {
		bodyWidth = 0
	}

	if bodyWidth >= 90 {
		listWidth := bodyWidth - 3 - 38
		if listWidth < 46 {
			listWidth = 46
		}
		lines = append(lines, v.renderRows(bodyHeight, listWidth, styled)...)
		lines = append(lines, v.renderDetails(38, bodyHeight, styled)...)
	} else {
		lines = append(lines, v.renderRows(bodyHeight, bodyWidth, styled)...)
	}

	// prompt
	label, input, placeholder := v.Prompt()
	prompt := []span{{text: label, style: spanCyanBold}, {text: input, style: spanPlain}}
	if placeholder != "" {
		available := bodyWidth - runewidth.StringWidth(label) - 1
		if available > 0 {
			placeholder = truncateToWidth(placeholder, available)
		}
		prompt = append(prompt, span{text: placeholder, style: spanDim})
	}
	lines = append(lines, renderLine(inset, prompt, maxWidth, styled))
	// footer
	lines = append(lines, renderLine(inset, v.footerSpans(), maxWidth, styled))
	return lines
}

func (v *View) footerSpans() []span {
	stopStyle := spanDim
	if row := v.SelectedRow(); row != nil && row.StatusActive {
		stopStyle = spanBold
	}
	spans := []span{
		{text: "↑↓", style: spanBold}, {text: " navigate  ", style: spanDim},
		{text: "enter", style: spanBold}, {text: " open  ", style: spanDim},
	}
	if binding, ok := v.shortcutHint(ShortcutHintSearch, "ctrl+f"); ok {
		spans = append(spans, span{text: binding, style: spanBold}, span{text: " search  ", style: spanDim})
	}
	if binding, ok := v.shortcutHint(ShortcutHintToggleGrouping, "ctrl+s"); ok {
		spans = append(spans, span{text: binding, style: spanBold}, span{text: " group  ", style: spanDim})
	}
	if binding, ok := v.shortcutHint(ShortcutHintRename, "ctrl+r"); ok {
		spans = append(spans, span{text: binding, style: spanBold}, span{text: " rename  ", style: spanDim})
	}
	if binding, ok := v.shortcutHint(ShortcutHintStop, "ctrl+x"); ok {
		spans = append(spans, span{text: binding, style: stopStyle}, span{text: " stop  ", style: spanDim})
	}
	spans = append(spans, span{text: "esc", style: spanBold}, span{text: " back", style: spanDim})
	return spans
}

func formatSummary(needsYou, working, ready int) string {
	attention := formatCount(needsYou) + " need input"
	return attention + "   " + formatCount(working) + " working   " + formatCount(ready) + " ready"
}

func formatCount(count int) string {
	return itoa(count)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [20]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	result := string(digits[index:])
	if negative {
		return "-" + result
	}
	return result
}

// renderRows renders the list body (group headers + rows), mirroring Rust
// AgentsOverviewView::render_rows including scroll-to-selection behavior.
func (v *View) renderRows(height, listWidth int, styled bool) []string {
	visible := v.VisibleIndices()
	if len(visible) == 0 {
		return nil
	}
	projectGrouping := !v.State.StatusGrouping
	selPos := 0
	for i, index := range visible {
		if index == v.Selected {
			selPos = i
			break
		}
	}

	// Walk back from the selection while the accumulated height fits.
	first := selPos
	accumulated := 1
	for first > 0 {
		previous := &v.Rows[visible[first-1]]
		current := &v.Rows[visible[first]]
		groupChanged := (projectGrouping && previous.CWD != current.CWD) ||
			(!projectGrouping && previous.Group != current.Group)
		added := 1
		if groupChanged {
			added = 1 + 1 // group header + separator
		}
		if accumulated+added > height {
			break
		}
		accumulated += added
		first--
	}

	lines := make([]string, 0, height)
	var previousGroup *string
	for _, index := range visible[first:] {
		if len(lines) >= height {
			break
		}
		row := &v.Rows[index]
		group := row.CWD
		if !projectGrouping {
			group = row.Group.Label()
		}
		if previousGroup == nil || *previousGroup != group {
			if previousGroup != nil {
				if len(lines) >= height {
					break
				}
				lines = append(lines, "")
			}
			if len(lines) >= height {
				break
			}
			count := 0
			for i := range v.Rows {
				if projectGrouping {
					if v.Rows[i].CWD == row.CWD {
						count++
					}
				} else if v.Rows[i].Group == row.Group {
					count++
				}
			}
			lines = append(lines, renderLine("", []span{
				{text: group, style: spanBold},
				{text: "  " + itoa(count), style: spanDim},
			}, listWidth, styled))
			previousGroup = &group
		}
		if len(lines) >= height {
			break
		}
		lines = append(lines, renderLine("", v.renderRowSpans(index, projectGrouping), listWidth, styled))
	}
	return lines
}

func (v *View) renderRowSpans(index int, projectGrouping bool) []span {
	row := &v.Rows[index]
	marker := span{text: " ", style: spanPlain}
	if v.Selected == index {
		marker = span{text: "›", style: spanCyanBold}
	}
	spans := []span{
		marker,
		{text: " ", style: spanPlain},
		{text: row.Group.Dot(), style: groupDotStyle(row.Group)},
		{text: " ", style: spanPlain},
		{text: row.Title(), style: spanPlain},
	}
	if row.IsCurrent {
		spans = append(spans, span{text: "  current", style: spanDim})
	}
	if projectGrouping {
		spans = append(spans, span{text: "  " + row.Group.Label(), style: spanDim})
	}
	return spans
}

// renderDetails renders the task details pane (wide terminals), mirroring
// Rust AgentsOverviewView::render_details.
func (v *View) renderDetails(width, height int, styled bool) []string {
	row := v.SelectedRow()
	if row == nil {
		return nil
	}
	var lines [][]span
	lines = append(lines, []span{{text: "Task details", style: spanBold}})
	lines = append(lines, nil)
	lines = append(lines, []span{{text: row.Title(), style: spanBold}})
	lines = append(lines, []span{
		{text: row.Group.Dot(), style: groupDotStyle(row.Group)},
		{text: " ", style: spanPlain},
		{text: row.Group.Label(), style: spanPlain},
	})
	lines = append(lines, nil)
	lines = append(lines, []span{{text: "Project", style: spanDim}})
	lines = append(lines, []span{{text: row.CWD, style: spanPlain}})
	if strings.TrimSpace(row.GitBranch) != "" {
		lines = append(lines, nil)
		lines = append(lines, []span{{text: "Branch", style: spanDim}})
		lines = append(lines, []span{{text: strings.TrimSpace(row.GitBranch), style: spanPlain}})
	}
	lines = append(lines, nil)
	lines = append(lines, []span{{text: "Latest activity", style: spanDim}})
	preview := strings.TrimSpace(row.Preview)
	if preview == "" {
		preview = "No activity yet."
	}
	for _, wrapped := range wordWrap(preview, width) {
		lines = append(lines, []span{{text: wrapped, style: spanPlain}})
	}

	out := make([]string, 0, height)
	lines = append(lines, nil)
	for _, spans := range lines {
		if len(out) >= height {
			break
		}
		out = append(out, renderLine("", spans, width, styled))
	}
	return out
}

// CursorColumn returns the terminal column for the input cursor, mirroring
// Rust AgentsOverviewView::cursor_pos (prompt line, bottom-2).
func (v *View) CursorColumn(termWidth int) int {
	if v == nil {
		return 0
	}
	label, input, _ := v.Prompt()
	column := 2 + displayWidth(label) + displayWidth(input)
	if column > termWidth-3 {
		column = termWidth - 3
	}
	return column
}

func displayWidth(value string) int {
	return runewidth.StringWidth(value)
}

func truncateToWidth(value string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	total := 0
	var builder strings.Builder
	for _, r := range []rune(value) {
		runeWidth := runewidth.RuneWidth(r)
		if total+runeWidth > maxWidth {
			break
		}
		builder.WriteRune(r)
		total += runeWidth
	}
	return builder.String()
}

func wordWrap(value string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{value}
	}
	words := strings.Fields(value)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	current := ""
	for _, word := range words {
		wordWidth := displayWidth(word)
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if displayWidth(candidate) > maxWidth && current != "" {
			lines = append(lines, current)
			current = word
			if wordWidth > maxWidth {
				for displayWidth(word) > maxWidth {
					part := truncateToWidth(word, maxWidth)
					if part == "" {
						break
					}
					lines = append(lines, part)
					word = word[len(part):]
				}
				current = word
			}
			continue
		}
		if displayWidth(candidate) > maxWidth && current == "" {
			// single over-long word: hard-split
			remaining := word
			for displayWidth(remaining) > maxWidth {
				part := truncateToWidth(remaining, maxWidth)
				if part == "" {
					break
				}
				lines = append(lines, part)
				remaining = remaining[len(part):]
			}
			current = remaining
			continue
		}
		current = candidate
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}
