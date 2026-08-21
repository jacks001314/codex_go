package tui

import (
	"path/filepath"
	"sort"
	"strings"

	"codex_go/utils"
)

// Rust parity: codex-rs/tui/src/diff_render.rs.

const (
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiGreen = "\x1b[32m"
	ansiRed   = "\x1b[31m"
	ansiReset = "\x1b[0m"
	// Combined fg+bg styles for git-style diff lines: the whole line is green
	// on a dark-green background for additions and red on dark-red for deletions.
	ansiAddLine = "\x1b[32;48;5;22m"
	ansiDelLine = "\x1b[31;48;5;52m"
)

type DiffLineType int

const (
	DiffLineContext DiffLineType = iota
	DiffLineInsert
	DiffLineDelete
)

type DiffRow struct {
	Path     string
	MovePath string
	Added    int
	Removed  int
	Change   FileChange
}

func CreateDiffSummary(changes map[string]FileChange, cwd string, wrapCols int) []string {
	rows := CollectDiffRows(changes)
	return RenderChangesBlock(rows, wrapCols, cwd)
}

func CollectDiffRows(changes map[string]FileChange) []DiffRow {
	rows := make([]DiffRow, 0, len(changes))
	for path, change := range changes {
		added, removed := FileChangeLineCounts(change)
		rows = append(rows, DiffRow{
			Path:     path,
			MovePath: change.MovePath,
			Added:    added,
			Removed:  removed,
			Change:   change,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Path < rows[j].Path
	})
	return rows
}

func FileChangeLineCounts(change FileChange) (int, int) {
	switch change.Type {
	case FileChangeAdd:
		return countContentLines(change.Content), 0
	case FileChangeDelete:
		return 0, countContentLines(change.Content)
	case FileChangeUpdate:
		return CalculateAddRemoveFromDiff(change.UnifiedDiff)
	default:
		return 0, 0
	}
}

func CalculateAddRemoveFromDiff(unifiedDiff string) (int, int) {
	added := 0
	removed := 0
	for _, line := range strings.Split(unifiedDiff, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") {
			added++
		} else if strings.HasPrefix(line, "-") {
			removed++
		}
	}
	return added, removed
}

func RenderChangesBlock(rows []DiffRow, wrapCols int, cwd string) []string {
	if wrapCols <= 0 {
		wrapCols = 80
	}
	totalAdded := 0
	totalRemoved := 0
	for _, row := range rows {
		totalAdded += row.Added
		totalRemoved += row.Removed
	}
	lines := []string{}
	switch len(rows) {
	case 0:
		return []string{"No file changes"}
	case 1:
		row := rows[0]
		verb := "Edited"
		switch row.Change.Type {
		case FileChangeAdd:
			verb = "Added"
		case FileChangeDelete:
			verb = "Deleted"
		}
		lines = append(lines, ansiDim+"\u2022 "+ansiReset+ansiBold+verb+ansiReset+" "+renderDiffPath(row, cwd)+" "+RenderLineCountSummary(row.Added, row.Removed))
	default:
		noun := "files"
		if len(rows) == 1 {
			noun = "file"
		}
		lines = append(lines, ansiDim+"\u2022 "+ansiReset+ansiBold+"Edited"+ansiReset+" "+FormatInt(int64(len(rows)))+" "+noun+" "+RenderLineCountSummary(totalAdded, totalRemoved))
	}
	for index, row := range rows {
		if index > 0 {
			lines = append(lines, "")
		}
		if len(rows) > 1 {
			lines = append(lines, ansiDim+"  \u2514 "+ansiReset+renderDiffPath(row, cwd)+" "+RenderLineCountSummary(row.Added, row.Removed))
		}
		// Saturate the content width so extremely narrow terminals stay
		// renderable (Rust #38075).
		contentWidth := wrapCols - 4
		if contentWidth < 0 {
			contentWidth = 0
		}
		for _, line := range RenderFileChange(row.Change, contentWidth) {
			lines = append(lines, "    "+line)
		}
	}
	return lines
}

func RenderLineCountSummary(added int, removed int) string {
	return "(" + ansiGreen + "+" + FormatInt(int64(added)) + ansiReset + " " + ansiRed + "-" + FormatInt(int64(removed)) + ansiReset + ")"
}

func RenderFileChange(change FileChange, width int) []string {
	if width < 0 {
		width = 80
	}
	switch change.Type {
	case FileChangeAdd:
		return renderWholeFileChange(change.Content, DiffLineInsert, width)
	case FileChangeDelete:
		return renderWholeFileChange(change.Content, DiffLineDelete, width)
	case FileChangeUpdate:
		return renderUnifiedDiff(change.UnifiedDiff, width)
	default:
		return nil
	}
}

func renderWholeFileChange(content string, lineType DiffLineType, width int) []string {
	lines := contentLines(content)
	numberWidth := LineNumberWidth(len(lines))
	out := []string{}
	for index, line := range lines {
		out = append(out, renderWrappedDiffLine(index+1, lineType, line, width, numberWidth)...)
	}
	return out
}

func renderUnifiedDiff(unifiedDiff string, width int) []string {
	out := []string{}
	oldLine := 0
	newLine := 0
	numberWidth := 1
	for _, raw := range strings.Split(unifiedDiff, "\n") {
		if raw == "" || strings.HasPrefix(raw, "diff ") || strings.HasPrefix(raw, "index ") || strings.HasPrefix(raw, "+++") || strings.HasPrefix(raw, "---") {
			continue
		}
		if strings.HasPrefix(raw, "@@") {
			oldStart, newStart := parseUnifiedHunkHeader(raw)
			oldLine = oldStart
			newLine = newStart
			out = append(out, raw)
			continue
		}
		switch {
		case strings.HasPrefix(raw, "+"):
			out = append(out, renderWrappedDiffLine(newLine, DiffLineInsert, strings.TrimPrefix(raw, "+"), width, numberWidth)...)
			newLine++
		case strings.HasPrefix(raw, "-"):
			out = append(out, renderWrappedDiffLine(oldLine, DiffLineDelete, strings.TrimPrefix(raw, "-"), width, numberWidth)...)
			oldLine++
		default:
			text := strings.TrimPrefix(raw, " ")
			lineNumber := newLine
			if lineNumber == 0 {
				lineNumber = oldLine
			}
			out = append(out, renderWrappedDiffLine(lineNumber, DiffLineContext, text, width, numberWidth)...)
			oldLine++
			newLine++
		}
	}
	return out
}

func renderWrappedDiffLine(lineNumber int, lineType DiffLineType, text string, width int, numberWidth int) []string {
	sign := " "
	lineStyle := ""
	switch lineType {
	case DiffLineInsert:
		sign = "+"
		lineStyle = ansiAddLine
	case DiffLineDelete:
		sign = "-"
		lineStyle = ansiDelLine
	}
	prefix := leftPadInt(lineNumber, numberWidth) + " "
	prefix += sign + " "
	wrapped := AdaptiveWrapLine(text, WrapOptions{
		Width:            width,
		InitialIndent:    prefix,
		SubsequentIndent: strings.Repeat(" ", DisplayWidth(prefix)),
		BreakWords:       true,
	})
	if lineStyle != "" {
		for i, line := range wrapped {
			wrapped[i] = lineStyle + line + ansiReset
		}
	}
	return wrapped
}

func LineNumberWidth(maxLineNumber int) int {
	if maxLineNumber < 1 {
		return 1
	}
	width := 0
	for maxLineNumber > 0 {
		width++
		maxLineNumber /= 10
	}
	return width
}

func parseUnifiedHunkHeader(header string) (int, int) {
	fields := strings.Fields(header)
	oldStart := 0
	newStart := 0
	if len(fields) >= 3 {
		oldStart = parseHunkStart(fields[1], '-')
		newStart = parseHunkStart(fields[2], '+')
	}
	return oldStart, newStart
}

func parseHunkStart(field string, prefix byte) int {
	field = strings.TrimSpace(field)
	if field == "" || field[0] != prefix {
		return 0
	}
	field = field[1:]
	if comma := strings.IndexByte(field, ','); comma >= 0 {
		field = field[:comma]
	}
	value := 0
	for _, r := range field {
		if r < '0' || r > '9' {
			return value
		}
		value = value*10 + int(r-'0')
	}
	return value
}

func renderDiffPath(row DiffRow, cwd string) string {
	path := DisplayDiffPath(row.Path, cwd)
	if row.MovePath != "" {
		path += " -> " + DisplayDiffPath(row.MovePath, cwd)
	}
	return path
}

func DisplayDiffPath(path string, cwd string) string {
	if relative, ok := utils.CrossPlatformRelative(cwd, path); ok {
		return relative
	}
	if cwd == "" || path == "" {
		return filepath.Clean(path)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	absCWD, err := filepath.Abs(cwd)
	if err != nil {
		return filepath.Clean(path)
	}
	if rel, err := filepath.Rel(absCWD, absPath); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.Clean(path)
}

func countContentLines(content string) int {
	return len(contentLines(content))
}

func contentLines(content string) []string {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func leftPadInt(value int, width int) string {
	text := FormatInt(int64(value))
	for DisplayWidth(text) < width {
		text = " " + text
	}
	return text
}
