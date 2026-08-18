package agentsoverview

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Render produces the plain-text dashboard lines for the given terminal
// dimensions, mirroring Rust AgentsOverviewView::render. Lines are unstyled;
// the terminal front-end may apply ANSI styling per line/segment.
func (v *View) Render(termWidth, termHeight int) []string {
	if v == nil || termWidth < 12 || termHeight < 8 {
		return nil
	}
	inset := func(x int, line string) string {
		if x <= 0 {
			return line
		}
		padding := x
		lineWidth := displayWidth(line)
		if padding+lineWidth > termWidth {
			line = truncateToWidth(line, termWidth-padding)
		}
		return strings.Repeat(" ", padding) + line
	}

	lines := make([]string, 0, termHeight)
	// header
	lines = append(lines, inset(2, "Agent command center"))
	// summary
	needsYou, working, ready := v.Counts()
	lines = append(lines, inset(2, formatSummary(needsYou, working, ready)))
	// divider
	dividerWidth := termWidth - 4
	if dividerWidth < 0 {
		dividerWidth = 0
	}
	lines = append(lines, inset(2, strings.Repeat("─", dividerWidth)))

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
		lines = append(lines, v.renderRows(bodyHeight, listWidth)...)
		lines = append(lines, v.renderDetails(38, bodyHeight)...)
	} else {
		lines = append(lines, v.renderRows(bodyHeight, bodyWidth)...)
	}

	// prompt
	label, input, placeholder := v.Prompt()
	prompt := label + input
	if placeholder != "" {
		available := bodyWidth - displayWidth(label) - 1
		if available > 0 {
			placeholder = truncateToWidth(placeholder, available)
		}
		prompt += placeholder
	}
	lines = append(lines, inset(2, prompt))
	// footer
	lines = append(lines, inset(2, "↑↓ navigate  enter open  ctrl+f search  ctrl+s group  ctrl+r rename  ctrl+x stop  esc back"))
	return lines
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
func (v *View) renderRows(height, listWidth int) []string {
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
			lines = append(lines, group+"  "+itoa(count))
			previousGroup = &group
		}
		if len(lines) >= height {
			break
		}
		lines = append(lines, v.renderRowLine(index, projectGrouping, listWidth))
	}
	return lines
}

func (v *View) renderRowLine(index int, projectGrouping bool, maxWidth int) string {
	row := &v.Rows[index]
	marker := " "
	if v.Selected == index {
		marker = "›"
	}
	title := row.Title()
	current := ""
	if row.IsCurrent {
		current = "  current"
	}
	line := marker + " " + row.Group.Dot() + " " + title + current
	if projectGrouping {
		line += "  " + row.Group.Label()
	}
	if displayWidth(line) > maxWidth {
		line = truncateToWidth(line, maxWidth)
	}
	return line
}

// renderDetails renders the task details pane (wide terminals), mirroring
// Rust AgentsOverviewView::render_details.
func (v *View) renderDetails(width, height int) []string {
	row := v.SelectedRow()
	if row == nil {
		return nil
	}
	var lines []string
	lines = append(lines, "Task details")
	lines = append(lines, "")
	lines = append(lines, row.Title())
	lines = append(lines, row.Group.Dot()+" "+row.Group.Label())
	lines = append(lines, "")
	lines = append(lines, "Project")
	lines = append(lines, row.CWD)
	if strings.TrimSpace(row.GitBranch) != "" {
		lines = append(lines, "")
		lines = append(lines, "Branch")
		lines = append(lines, strings.TrimSpace(row.GitBranch))
	}
	lines = append(lines, "")
	lines = append(lines, "Latest activity")
	preview := strings.TrimSpace(row.Preview)
	if preview == "" {
		preview = "No activity yet."
	}
	lines = append(lines, wordWrap(preview, width)...)

	out := make([]string, 0, height)
	lines = append(lines, "")
	for _, line := range lines {
		if len(out) >= height {
			break
		}
		if displayWidth(line) > width {
			line = truncateToWidth(line, width)
		}
		out = append(out, line)
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
