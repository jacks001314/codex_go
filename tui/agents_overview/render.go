package agentsoverview

import (
	"strings"

	"github.com/mattn/go-runewidth"

	"codex_go/tui/footerhint"
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

	// renderLine renders the footer inside the inset, so pack the hints to the
	// same content width the rows are drawn into.
	footerWidth := maxWidth - len(inset)
	if footerWidth < 1 {
		footerWidth = 1
	}
	footerRows := v.footerHintRows(footerWidth)
	// header + summary + divider + editor + footer rows
	bodyHeight := termHeight - 4 - len(footerRows)
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

	// metadata editor (search or rename); browsing renders no editor row
	// (Rust agents_overview_render.rs after #45255)
	label, input, placeholder := v.Prompt()
	if label != "" || input != "" || placeholder != "" {
		prompt := []span{{text: label, style: spanCyanBold}, {text: input, style: spanPlain}}
		if placeholder != "" {
			available := bodyWidth - runewidth.StringWidth(label) - 1
			if available > 0 {
				placeholder = truncateToWidth(placeholder, available)
			}
			prompt = append(prompt, span{text: placeholder, style: spanDim})
		}
		lines = append(lines, renderLine(inset, prompt, maxWidth, styled))
	}
	// footer
	for _, row := range footerRows {
		lines = append(lines, renderLine(inset, row, maxWidth, styled))
	}
	return lines
}

// footerHints returns each footer hint as its own span group, in Rust's order
// (agents_overview_render.rs).
func (v *View) footerHints() [][]span {
	stopStyle := spanDim
	if row := v.SelectedRow(); row != nil && row.StatusActive {
		stopStyle = spanBold
	}
	// Rust #45255: Enter opens the selected task; Right only opens when the
	// list owns the keys, which the footer no longer advertises.
	openHint := "enter"
	hints := [][]span{
		{{text: "\u2191\u2193", style: spanBold}, {text: " navigate", style: spanDim}},
		{{text: openHint, style: spanBold}, {text: " open", style: spanDim}},
	}
	if binding, ok := v.shortcutHint(ShortcutHintNewTask, "n"); ok {
		hints = append(hints, []span{{text: binding, style: spanBold}, {text: " new", style: spanDim}})
	}
	if binding, ok := v.shortcutHint(ShortcutHintSearch, "f"); ok {
		hints = append(hints, []span{{text: binding, style: spanBold}, {text: " search", style: spanDim}})
	}
	if binding, ok := v.shortcutHint(ShortcutHintToggleGrouping, "g"); ok {
		// Rust #44957: the footer reports the active grouping mode.
		hints = append(hints, []span{{text: binding, style: spanBold}, {text: " " + v.State.Grouping.Label(), style: spanDim}})
	}
	if binding, ok := v.shortcutHint(ShortcutHintRename, "r"); ok {
		hints = append(hints, []span{{text: binding, style: spanBold}, {text: " rename", style: spanDim}})
	}
	if binding, ok := v.shortcutHint(ShortcutHintStop, "x"); ok {
		hints = append(hints, []span{{text: binding, style: stopStyle}, {text: " stop", style: spanDim}})
	}
	if binding, ok := v.shortcutHint(ShortcutHintHide, "h"); ok {
		hints = append(hints, []span{{text: binding, style: spanBold}, {text: " hide", style: spanDim}})
	}
	if binding, ok := v.shortcutHint(ShortcutHintArchive, "a"); ok {
		hints = append(hints, []span{{text: binding, style: spanBold}, {text: " archive", style: spanDim}})
	}
	if binding, ok := v.shortcutHint(ShortcutHintDelete, "delete"); ok {
		hints = append(hints, []span{{text: binding, style: spanBold}, {text: " delete", style: spanDim}})
	}
	// Rust #45255: Esc only cancels metadata editing, so the list stays open and
	// the hint appears only while editing; quitting is always advertised.
	if v != nil && (v.State.Searching || v.State.Renaming) {
		hints = append(hints, []span{{text: "esc", style: spanBold}, {text: " cancel", style: spanDim}})
	}
	hints = append(hints, []span{{text: "ctrl-c", style: spanBold}, {text: " quit", style: spanDim}})
	return hints
}

// footerHintRows packs the footer hints into display rows, choosing the
// separator width from whether every hint fits on one line and word-wrapping a
// hint that is wider than the terminal (Rust footer_hint::wrap_hint_rows +
// word_wrap_lines).
func (v *View) footerHintRows(maxWidth int) [][]span {
	hints := v.footerHints()
	if len(hints) == 0 {
		return nil
	}
	total := 0
	for _, hint := range hints {
		total += spansWidth(hint)
	}
	separator := " "
	if total+(len(hints)-1)*2 <= maxWidth {
		separator = "  "
	}
	rows := footerhint.WrapHintRows(hints, maxWidth, len(separator), spansWidth)
	out := make([][]span, 0, len(rows))
	for _, row := range rows {
		joined := make([]span, 0, len(row)*3)
		for index, hint := range row {
			if index > 0 {
				joined = append(joined, span{text: separator, style: spanDim})
			}
			joined = append(joined, hint...)
		}
		if maxWidth <= 0 || spansWidth(joined) <= maxWidth {
			out = append(out, joined)
			continue
		}
		out = append(out, splitSpansToWidth(joined, maxWidth)...)
	}
	return out
}

// splitSpansToWidth hard-splits a composed hint at display-width boundaries,
// preserving each span's style (Rust word_wrap_lines' over-long-word path).
// WrapHintRows only emits a row wider than the terminal when a single hint is
// oversized, so this never runs for a multi-hint row.
func splitSpansToWidth(spans []span, maxWidth int) [][]span {
	if maxWidth <= 0 {
		return [][]span{spans}
	}
	lines := make([][]span, 0, 2)
	current := make([]span, 0, len(spans))
	used := 0
	for _, s := range spans {
		if s.raw {
			continue
		}
		text := s.text
		for text != "" {
			if used >= maxWidth {
				lines = append(lines, current)
				current = make([]span, 0, len(spans))
				used = 0
			}
			part, rest := cutByWidth(text, maxWidth-used)
			if part == "" {
				break
			}
			current = append(current, span{text: part, style: s.style, color: s.color})
			used += ansiAwareWidth(part)
			text = rest
		}
	}
	if len(current) > 0 || len(lines) == 0 {
		lines = append(lines, current)
	}
	return lines
}

// cutByWidth returns the longest prefix of value fitting maxWidth display
// columns plus the remainder.
func cutByWidth(value string, maxWidth int) (string, string) {
	if maxWidth <= 0 {
		return "", value
	}
	used := 0
	for index, r := range value {
		width := runewidth.RuneWidth(r)
		if used+width > maxWidth {
			return value[:index], value[index:]
		}
		used += width
	}
	return value, ""
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
	grouping := v.State.Grouping
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
		groupChanged := !v.sameGroup(grouping, visible[first-1], visible[first])
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
		group := v.groupHeading(grouping, index)
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
				if v.sameGroup(grouping, i, index) {
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
		lines = append(lines, renderLine("", v.renderRowSpans(index, grouping != GroupingStatus), listWidth, styled))
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
		v.titleSpan(row.ThreadID, row.Title(), spanPlain),
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
	lines = append(lines, []span{v.titleSpan(row.ThreadID, row.Title(), spanBold)})
	lines = append(lines, []span{
		{text: row.Group.Dot(), style: groupDotStyle(row.Group)},
		{text: " ", style: spanPlain},
		{text: row.Group.Label(), style: spanPlain},
	})
	lines = append(lines, nil)
	lines = append(lines, []span{{text: "Project", style: spanDim}})
	lines = append(lines, []span{{text: row.CWD, style: spanPlain}})
	// Rust #44957: task details show the task's model.
	lines = append(lines, []span{
		{text: "Model: ", style: spanDim},
		{text: ModelName(row.Model), style: spanPlain},
	})
	// Rust #44970: token totals and estimated usage render after the model.
	usage := v.usageLinesFor(row.ThreadID)
	for _, rendered := range usage {
		lines = append(lines, []span{{text: rendered, style: spanDim}})
	}
	if strings.TrimSpace(row.GitBranch) != "" {
		lines = append(lines, nil)
		lines = append(lines, []span{{text: "Branch", style: spanDim}})
		lines = append(lines, []span{{text: strings.TrimSpace(row.GitBranch), style: spanPlain}})
	}
	// Rust #44752: the details pane also shows the task's last delivered agent
	// message, rendered from markdown with the task's working directory.
	if message := strings.TrimSpace(row.LastMessage); message != "" {
		lines = append(lines, nil)
		lines = append(lines, []span{{text: "Last message", style: spanDim}})
		rendered := v.RenderMarkdown != nil
		var messageLines []string
		if rendered {
			messageLines = v.RenderMarkdown(message, width)
			if len(messageLines) == 0 {
				rendered = false
			}
		}
		if len(messageLines) == 0 {
			messageLines = wrapPreviewLines(message, width)
		}
		for _, line := range messageLines {
			lines = append(lines, []span{{text: line, raw: rendered}})
		}
	}
	// promptStart marks where the original prompt begins; when usage is present
	// and the details would exceed the pane, the prompt is dropped so activity
	// and usage keep their lines (Rust #44970).
	promptStart := len(lines)
	lines = append(lines, nil)
	// Rust #44752: the bounded prompt preview keeps its explicit line breaks and
	// is limited to two rendered lines with an ellipsis marker.
	lines = append(lines, []span{{text: "Prompt", style: spanDim}})
	preview := PreviewMarkdown(row.Preview)
	if preview == "" {
		preview = "No prompt available."
	}
	// The renderer supplies styled lines; plain rendering strips the styling so
	// both modes show the parsed markdown text (Rust #44752).
	markdown := v.RenderMarkdown != nil && preview != "No prompt available."
	var prompt []string
	if markdown {
		prompt = v.RenderMarkdown(preview, width)
		if len(prompt) == 0 {
			markdown = false
		}
	}
	if len(prompt) == 0 {
		prompt = wrapPreviewLines(preview, width)
	}
	if len(prompt) > 2 {
		prompt = prompt[:2]
		prompt[1] = "\u2026"
	}
	for _, rendered := range prompt {
		lines = append(lines, []span{{text: rendered, raw: markdown}})
	}

	if len(usage) > 0 && len(lines) > height && promptStart < len(lines) {
		lines = lines[:promptStart]
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

// wrapPreviewLines word-wraps a preview while preserving its explicit line
// breaks and tabs (Rust #44752).
func wrapPreviewLines(value string, maxWidth int) []string {
	if value == "" {
		return nil
	}
	out := []string{}
	for _, raw := range strings.Split(value, "\n") {
		out = append(out, wordWrap(strings.TrimSuffix(raw, "\r"), maxWidth)...)
	}
	return out
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
