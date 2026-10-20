package agentsoverview

import (
	"fmt"
	"strings"
	"time"

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
	grouping := "Group: " + v.State.Grouping.DisplayLabel()
	if key, ok := v.shortcutHint(ShortcutHintToggleGrouping, "g"); ok && key != "" {
		grouping += "  " + key
	}
	lines = append(lines, renderLine(inset, []span{
		{text: "Agent command center", style: spanBold},
		{text: "  " + grouping, style: spanDim},
	}, maxWidth, styled))
	// status tabs: worktree creation reports progress in place of the tabs
	// (Rust #45276).
	if v.State.CreatingWorktree {
		lines = append(lines, renderLine(inset, []span{{text: "Creating worktree…", style: spanCyanBold}}, maxWidth, styled))
	} else {
		lines = append(lines, renderLine(inset, v.statusFilterTabSpans(maxWidth-len(inset)), maxWidth, styled))
	}
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
	if len(v.KeyChordHints) > 0 {
		footerRows = v.keyChordHintRows(footerWidth)
	}
	// header + summary + divider + editor + footer rows
	bodyHeight := termHeight - 4 - len(footerRows)
	if bodyHeight < 3 {
		bodyHeight = 3
	}
	bodyWidth := termWidth - 4
	if bodyWidth < 0 {
		bodyWidth = 0
	}

	// metadata editor (search or rename) above the task list; browsing shows
	// the search hint in the same row (Rust #46838).
	label, input, placeholder := v.Prompt()
	if label != "" || input != "" || placeholder != "" {
		if v.State.Renaming && input != "" {
			if available := bodyWidth - runewidth.StringWidth(label); available > 0 {
				input = v.singleLineViewport(available)
			}
		}
		prompt := []span{{text: label, style: spanCyanBold}, {text: input, style: spanPlain}}
		if placeholder != "" {
			available := bodyWidth - runewidth.StringWidth(label) - 1
			if available > 0 {
				placeholder = truncateToWidth(placeholder, available)
			}
			prompt = append(prompt, span{text: placeholder, style: spanDim})
		}
		lines = append(lines, renderLine(inset, prompt, maxWidth, styled))
	} else {
		searchHint := "Search unavailable"
		if key, ok := v.shortcutHint(ShortcutHintSearch, "f3"); ok && key != "" {
			searchHint = "Search tasks \u00b7 " + key
		}
		lines = append(lines, renderLine(inset, []span{{text: searchHint, style: spanDim}}, maxWidth, styled))
	}

	if v.State.Help {
		lines = append(lines, v.renderHelp(bodyWidth, styled)...)
	} else if bodyWidth >= 90 {
		listWidth := bodyWidth - 3 - 38
		if listWidth < 46 {
			listWidth = 46
		}
		lines = append(lines, v.renderRows(bodyHeight, listWidth, styled)...)
		lines = append(lines, v.renderDetails(38, bodyHeight, styled)...)
	} else {
		lines = append(lines, v.renderRows(bodyHeight, bodyWidth, styled)...)
	}

	// footer
	if len(v.KeyChordHints) > 0 {
		for _, row := range footerRows {
			lines = append(lines, renderLine(inset, row, maxWidth, styled))
		}
	} else if v.Notice != "" {
		lines = append(lines, renderLine(inset, []span{{text: v.Notice, style: spanDim}}, maxWidth, styled))
		for range max(len(footerRows)-1, 0) {
			lines = append(lines, "")
		}
	} else {
		for _, row := range footerRows {
			lines = append(lines, renderLine(inset, row, maxWidth, styled))
		}
	}
	return lines
}

// renderHelp renders the grouped task-shortcut reference (Rust #46882).
func (v *View) renderHelp(width int, styled bool) []string {
	if v == nil {
		return nil
	}
	hint := func(key string, fallback string) string {
		value, _ := v.shortcutHint(key, fallback)
		return value
	}
	lines := []string{renderLine("", []span{{text: "Task shortcuts", style: spanBold}}, width, styled)}
	group := func(title string, entries ...[2]string) {
		lines = append(lines, "")
		lines = append(lines, renderLine("", []span{{text: title, style: spanCyanBold}}, width, styled))
		for _, entry := range entries {
			if strings.TrimSpace(entry[0]) == "" {
				continue
			}
			lines = append(lines, renderLine("  ", []span{
				{text: entry[0], style: spanBold},
				{text: " " + entry[1], style: spanDim},
			}, width, styled))
		}
	}
	group("Navigate",
		[2]string{hint("move_up", "\u2191"), "Up"},
		[2]string{hint("move_down", "\u2193"), "Down"},
		[2]string{hint("accept", "enter"), "Open"},
		[2]string{hint("page_up", "pgup"), "Page up"},
		[2]string{hint("page_down", "pgdown"), "Page down"},
	)
	taskEntries := [][2]string{
		[2]string{hint(ShortcutHintNewTask, "n"), "New"},
	}
	if v.worktreesEnabled {
		taskEntries = append(taskEntries, [2]string{hint(ShortcutHintNewWorktree, "w"), "New worktree"})
	}
	taskEntries = append(taskEntries,
		[2]string{hint("resume", "o"), "Resume"},
		[2]string{hint(ShortcutHintRename, "r"), "Rename"},
		[2]string{hint(ShortcutHintStop, "x"), "Stop"},
		[2]string{hint(ShortcutHintArchive, "a"), "Archive"},
		[2]string{hint(ShortcutHintHide, "h"), "Hide"},
		[2]string{hint(ShortcutHintDelete, "backspace"), "Delete"},
	)
	group("Tasks", taskEntries...)
	if v.CanToggleSelectedPin() {
		// The shared pin entry sits at the end of the task group.
		lines = append(lines, renderLine("  ", []span{
			{text: hint(ShortcutHintTogglePin, "p"), style: spanBold},
			{text: " Pin/unpin", style: spanDim},
		}, width, styled))
	}
	group("View",
		[2]string{strings.TrimSpace(v.FilterHint), "Filter"},
		[2]string{hint(ShortcutHintSearch, "f3"), "Search"},
		[2]string{hint(ShortcutHintToggleGrouping, "g"), "Group"},
	)
	return lines
}

func (v *View) keyChordHintRows(maxWidth int) [][]span {
	if v == nil || len(v.KeyChordHints) == 0 {
		return nil
	}
	hints := make([][]span, 0, len(v.KeyChordHints))
	for _, hint := range v.KeyChordHints {
		keys := strings.TrimSpace(hint.Keys)
		action := strings.TrimSpace(hint.Action)
		if keys == "" {
			continue
		}
		row := []span{{text: keys, style: spanBold}}
		if action != "" {
			row = append(row, span{text: " " + action, style: spanDim})
		}
		hints = append(hints, row)
	}
	if len(hints) == 0 {
		return nil
	}
	// Rust renders the chord hint as a single footer line (hint_line) and lets
	// the footer area clip overflow, so keep every hint on one row here. The
	// normal footer wraps because footer_hint::wrap_hint_rows applies there.
	joined := make([]span, 0, len(hints)*3)
	for index, hint := range hints {
		if index > 0 {
			joined = append(joined, span{text: "  ", style: spanDim})
		}
		joined = append(joined, hint...)
	}
	return [][]span{joined}
}

// statusFilterTabSpans renders the counted status tabs and, on wide terminals,
// the active filter shortcut at the right edge (Rust #46839 render.rs).
func (v *View) statusFilterTabSpans(width int) []span {
	if v == nil {
		return nil
	}
	if width < 1 {
		width = 1
	}
	labels := v.StatusFilterLabels()
	hint := strings.TrimSpace(v.FilterHint)
	hintWidth := runewidth.StringWidth(hint)
	tabWidth := width
	if hint != "" && width >= 80 && hintWidth+2 < width {
		tabWidth = width - hintWidth - 2
	} else {
		hint = ""
	}
	cells := statusFilterTabCells(labels, v.StatusFilterIndex(), tabWidth)
	spans := make([]span, 0, len(cells)*2+2)
	column := 0
	for _, cell := range cells {
		if cell.Start > column {
			spans = append(spans, span{text: strings.Repeat(" ", cell.Start-column), style: spanPlain})
			column = cell.Start
		}
		style := spanDim
		if cell.Active {
			style = spanCyanBold
		}
		spans = append(spans, span{text: cell.Text, style: style})
		column += runewidth.StringWidth(cell.Text)
	}
	if column < tabWidth {
		// FilledTabBarText pads to width; preserve that padding before the hint.
		spans = append(spans, span{text: strings.Repeat(" ", tabWidth-column), style: spanPlain})
	}
	if hint != "" {
		spans = append(spans,
			span{text: "  ", style: spanPlain},
			span{text: hint, style: spanDim},
		)
	}
	return spans
}

// statusFilterTabCell is one visible tab or overflow marker.
type statusFilterTabCell struct {
	Start  int
	Width  int
	Text   string
	Active bool
}

// statusFilterTabCells mirrors Rust's render_filled_tab_bar, kept local to
// avoid a dependency cycle through the bottom-pane package.
func statusFilterTabCells(labels []string, activeIdx int, width int) []statusFilterTabCell {
	if len(labels) == 0 || width <= 0 {
		return nil
	}
	if activeIdx < 0 {
		activeIdx = 0
	}
	if activeIdx >= len(labels) {
		activeIdx = len(labels) - 1
	}
	const markerColumns = 2
	widths := make([]int, len(labels))
	for index, label := range labels {
		widths[index] = runewidth.StringWidth(label) + 2
	}
	occupied := func(start int, end int) int {
		total := end - start - 1
		for _, cellWidth := range widths[start:end] {
			total += cellWidth
		}
		if start > 0 {
			total += markerColumns
		}
		if end < len(labels) {
			total += markerColumns
		}
		return total
	}
	start, end := activeIdx, activeIdx+1
	for start > 0 && occupied(start-1, end) <= width {
		start--
	}
	for end < len(labels) && occupied(start, end+1) <= width {
		end++
	}
	showLeft := start > 0 && width >= 5
	showRight := end < len(labels) && width >= 7
	right := width
	if showRight {
		right -= markerColumns
	}
	cells := make([]statusFilterTabCell, 0, end-start+2)
	x := 0
	if showLeft {
		cells = append(cells, statusFilterTabCell{Start: x, Width: 1, Text: "\u2039"})
		x += markerColumns
	}
	for index := start; index < end; index++ {
		cellWidth := min(widths[index], max(right-x, 0))
		text := truncateToWidthWithEllipsis(" "+labels[index]+" ", cellWidth)
		cells = append(cells, statusFilterTabCell{
			Start:  x,
			Width:  cellWidth,
			Text:   text,
			Active: index == activeIdx,
		})
		x += cellWidth + 1
	}
	if showRight {
		cells = append(cells, statusFilterTabCell{Start: width - 1, Width: 1, Text: "\u203a"})
	}
	return cells
}

func truncateToWidthWithEllipsis(value string, width int) string {
	spans := truncateSpansWithEllipsis([]span{{text: value, style: spanPlain}}, width)
	var builder strings.Builder
	for _, item := range spans {
		builder.WriteString(item.text)
	}
	return builder.String()
}

// footerHints returns each footer hint as its own span group, in Rust's order
// (agents_overview_render.rs).
func (v *View) footerHints() [][]span {
	if v != nil && v.State.Help {
		return [][]span{
			{{text: "esc", style: spanBold}, {text: " back", style: spanDim}},
			{{text: "ctrl-c", style: spanBold}, {text: " quit", style: spanDim}},
		}
	}
	if v != nil && v.State.CreatingWorktree {
		// Rust #45276: creating a worktree pauses the actions, so the footer
		// only reports progress and the quit shortcut.
		return [][]span{
			{{text: "Creating worktree…", style: spanBold}, {text: "  ctrl-c quit", style: spanDim}},
		}
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
	// Rust #45255: Esc only cancels metadata editing, so the list stays open and
	// the hint appears only while editing; quitting is always advertised.
	if v != nil && (v.State.Searching || v.State.Renaming) {
		hints = append(hints, []span{{text: "esc", style: spanBold}, {text: " cancel", style: spanDim}})
	}
	hints = append(hints, []span{{text: "?", style: spanBold}, {text: " help", style: spanDim}})
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
	if height > 0 {
		v.pageHeight = height
	}
	if len(visible) == 0 {
		message := "No matching tasks"
		switch {
		case v.Loading && len(v.Rows) == 0:
			message = "Loading tasks\u2026"
		case len(v.Rows) == 0:
			message = "No tasks yet"
		}
		return []string{renderLine("", []span{{text: message, style: spanDim}}, listWidth, styled)}
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
		lines = append(lines, renderLine("", v.renderRowSpans(index, grouping != GroupingStatus, listWidth), listWidth, styled))
	}
	return lines
}

func (v *View) renderRowSpans(index int, projectGrouping bool, width int) []span {
	row := &v.Rows[index]
	marker := span{text: " ", style: spanPlain}
	statusStyle := groupDotStyle(row.Group)
	if v.Selected == index {
		marker = span{text: "›", style: spanCyanBold}
		statusStyle = spanCyanBold
	}
	spans := []span{
		marker,
		{text: " ", style: spanPlain},
		{text: row.Group.Dot(), style: statusStyle},
		{text: " ", style: spanPlain},
		v.titleSpan(row.ThreadID, row.Title(), spanPlain),
	}
	if row.HasVoice {
		voiceStyle := spanPlain
		if v.Selected == index {
			voiceStyle = spanCyanBold
		}
		spans = append(spans, span{text: "  voice", style: voiceStyle})
	}
	if width >= 56 {
		const (
			updatedWidth = 9
			statusWidth  = 11
		)
		titleWidth := width - updatedWidth - statusWidth - 2
		if titleWidth < 1 {
			titleWidth = 1
		}
		if spansWidth(spans) > titleWidth {
			spans = truncateSpans(spans, titleWidth)
		}
		if used := spansWidth(spans); used < titleWidth {
			spans = append(spans, span{text: strings.Repeat(" ", titleWidth-used), style: spanPlain})
		}
		status := truncateToWidth(row.Group.StatusLabel(row.StatusType), statusWidth)
		spans = append(spans,
			span{text: status, style: spanDim},
			span{text: strings.Repeat(" ", max(statusWidth-runewidth.StringWidth(status), 1)), style: spanPlain},
		)
		if used := spansWidth(spans); used < width-updatedWidth {
			spans = append(spans, span{text: strings.Repeat(" ", width-updatedWidth-used), style: spanPlain})
		}
		updated := relativeUpdateTime(row.UpdatedAt)
		spans = append(spans, span{text: rightPadLeft(updated, updatedWidth), style: spanDim})
		return spans
	}
	if projectGrouping {
		spans = append(spans, span{text: "  " + row.Group.StatusLabel(row.StatusType), style: spanDim})
	}
	return spans
}

func relativeUpdateTime(updatedAt int64) string {
	if updatedAt <= 0 {
		return "-"
	}
	seconds := time.Now().Unix() - updatedAt
	if seconds <= 0 {
		return "now"
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds ago", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm ago", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%dh ago", hours)
	}
	return fmt.Sprintf("%dd ago", hours/24)
}

func rightPadLeft(value string, width int) string {
	value = truncateToWidth(value, width)
	if padding := width - runewidth.StringWidth(value); padding > 0 {
		return strings.Repeat(" ", padding) + value
	}
	return value
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
		{text: row.Group.StatusLabel(row.StatusType), style: spanPlain},
	})
	lines = append(lines, nil)
	// Rust #50727: the model and reasoning effort sit at the top of the task
	// details, and long values are ellipsis-truncated to keep one line.
	lines = append(lines, truncateSpansWithEllipsis([]span{
		{text: "Model: ", style: spanDim},
		{text: ModelName(row.Model), style: spanPlain},
	}, width))
	lines = append(lines, truncateSpansWithEllipsis([]span{
		{text: "Reasoning: ", style: spanDim},
		{text: ReasoningEffortName(row.ReasoningEffort), style: spanPlain},
	}, width))
	lines = append(lines, nil)
	lines = append(lines, []span{{text: "Project", style: spanDim}})
	lines = append(lines, []span{{text: row.CWD, style: spanPlain}})
	// Rust #44970: token totals and estimated usage render after the project.
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
