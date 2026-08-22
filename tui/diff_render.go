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
	// Rust fallback diff palette (diff_render.rs). Dark theme uses muted red/green
	// backgrounds; light theme uses GitHub-style pastels with tinted number cells.
	ansiBgAddDark     = "\x1b[48;2;33;58;43m"    // #213A2B
	ansiBgDelDark     = "\x1b[48;2;74;34;29m"    // #4A221D
	ansiBgAddLight    = "\x1b[48;2;218;251;225m" // #dafbe1
	ansiBgDelLight    = "\x1b[48;2;255;235;233m" // #ffebe9
	ansiBgAddNumLight = "\x1b[48;2;172;238;187m" // #aceebb
	ansiBgDelNumLight = "\x1b[48;2;255;206;203m" // #ffcecb
	ansiGutterFgLight = "\x1b[38;2;31;35;40m"    // #1f2328
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

func CreateDiffSummary(changes map[string]FileChange, cwd string, wrapCols int, theme string) []string {
	rows := CollectDiffRows(changes)
	return RenderChangesBlock(rows, wrapCols, cwd, theme)
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

func RenderChangesBlock(rows []DiffRow, wrapCols int, cwd string, theme string) []string {
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
		contentWidth := wrapCols - 4
		if contentWidth < 0 {
			contentWidth = 0
		}
		for _, line := range RenderFileChange(row.Change, contentWidth, theme, detectLangForPath(row.Path)) {
			lines = append(lines, "    "+line)
		}
	}
	return lines
}

func RenderLineCountSummary(added int, removed int) string {
	return "(" + ansiGreen + "+" + FormatInt(int64(added)) + ansiReset + " " + ansiRed + "-" + FormatInt(int64(removed)) + ansiReset + ")"
}

func RenderFileChange(change FileChange, width int, theme string, lang string) []string {
	if width < 0 {
		width = 80
	}
	switch change.Type {
	case FileChangeAdd:
		return renderWholeFileChange(change.Content, DiffLineInsert, width, theme, lang)
	case FileChangeDelete:
		return renderWholeFileChange(change.Content, DiffLineDelete, width, theme, lang)
	case FileChangeUpdate:
		return renderUnifiedDiff(change.UnifiedDiff, width, theme, lang)
	default:
		return nil
	}
}

func renderWholeFileChange(content string, lineType DiffLineType, width int, theme string, lang string) []string {
	lines := contentLines(content)
	numberWidth := LineNumberWidth(len(lines))
	out := []string{}
	for index, line := range lines {
		out = append(out, renderWrappedDiffLine(index+1, lineType, line, width, numberWidth, theme, lang)...)
	}
	return out
}

func renderUnifiedDiff(unifiedDiff string, width int, theme string, lang string) []string {
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
			out = append(out, renderWrappedDiffLine(newLine, DiffLineInsert, strings.TrimPrefix(raw, "+"), width, numberWidth, theme, lang)...)
			newLine++
		case strings.HasPrefix(raw, "-"):
			out = append(out, renderWrappedDiffLine(oldLine, DiffLineDelete, strings.TrimPrefix(raw, "-"), width, numberWidth, theme, lang)...)
			oldLine++
		default:
			text := strings.TrimPrefix(raw, " ")
			lineNumber := newLine
			if lineNumber == 0 {
				lineNumber = oldLine
			}
			out = append(out, renderWrappedDiffLine(lineNumber, DiffLineContext, text, width, numberWidth, theme, "")...)
			oldLine++
			newLine++
		}
	}
	return out
}

func renderWrappedDiffLine(lineNumber int, lineType DiffLineType, text string, width int, numberWidth int, theme string, lang string) []string {
	light := diffThemeIsLight(theme)
	level := diffColorLevel()
	sign := " "
	switch lineType {
	case DiffLineInsert:
		sign = "+"
	case DiffLineDelete:
		sign = "-"
	}
	num := leftPadInt(lineNumber, numberWidth)
	gutterDisplay := numberWidth + 3 // number + space + sign + space
	subjectWidth := width - gutterDisplay
	if subjectWidth < 1 {
		subjectWidth = 1
	}
	contentSegments := WrapLine(text, WrapOptions{Width: subjectWidth, BreakWords: true})
	if len(contentSegments) == 0 {
		contentSegments = []string{""}
	}
	out := make([]string, 0, len(contentSegments))
	indent := strings.Repeat(" ", gutterDisplay)
	for i, seg := range contentSegments {
		prefix := indent
		if i == 0 {
			prefix = buildDiffPrefix(lineType, num, sign, light, level)
		}
		out = append(out, prefix+styleDiffContent(seg, lang, theme, lineType, light, level)+ansiReset)
	}
	return out
}

func buildDiffPrefix(lineType DiffLineType, num string, sign string, light bool, level StdoutColorLevel) string {
	var sb strings.Builder
	if light {
		// Light theme: the line-number cell gets its own tinted background for
		// rich color levels; ANSI-16 keeps the number plain black on the
		// default background (Rust: style_gutter_for light_gutter_fg).
		switch level {
		case ColorTrue:
			sb.WriteString(ansiGutterFgLight)
		case ColorANSI256:
			sb.WriteString("\x1b[38;5;236m")
		default:
			sb.WriteString("\x1b[30m")
		}
		switch lineType {
		case DiffLineInsert:
			switch level {
			case ColorTrue:
				sb.WriteString(ansiBgAddNumLight)
			case ColorANSI256:
				sb.WriteString("\x1b[48;5;157m")
			}
		case DiffLineDelete:
			switch level {
			case ColorTrue:
				sb.WriteString(ansiBgDelNumLight)
			case ColorANSI256:
				sb.WriteString("\x1b[48;5;217m")
			}
		}
		sb.WriteString(num)
		sb.WriteString(ansiReset)
		sb.WriteString(" ")
		switch lineType {
		case DiffLineInsert:
			sb.WriteString(ansiGreen)
			sb.WriteString(sign)
			sb.WriteString(ansiReset)
		case DiffLineDelete:
			sb.WriteString(ansiRed)
			sb.WriteString(sign)
			sb.WriteString(ansiReset)
		default:
			sb.WriteString(" ")
		}
		sb.WriteString(" ")
		return sb.String()
	}
	// Dark theme: dim line number with no tinted cell.
	sb.WriteString(ansiDim)
	sb.WriteString(num)
	sb.WriteString(ansiReset)
	sb.WriteString(" ")
	if lineType == DiffLineContext {
		sb.WriteString(" ")
	} else {
		sb.WriteString(diffSignSGR(lineType, light, level))
		sb.WriteString(sign)
		sb.WriteString(ansiReset)
	}
	sb.WriteString(" ")
	return sb.String()
}

// styleDiffContent renders the wrapped content segment of a diff line with the
// resolved line background (git-style) and, when a source language is known,
// syntax highlighting whose token foregrounds are re-anchored onto the line
// background (Rust parity: push_wrapped_diff_line_with_syntax_and_style_context).
func styleDiffContent(content string, lang string, theme string, lineType DiffLineType, light bool, level StdoutColorLevel) string {
	bg := diffBgSGR(lineType, light, level)
	if lang == "" {
		return bg + diffFgSGR(lineType, light) + content
	}
	hl := HighlightCodeANSI(content, lang, theme)
	if hl == "" {
		return bg + diffFgSGR(lineType, light) + content
	}
	// Re-apply the line background after every SGR reset so chroma token colors
	// sit on top of the tinted background instead of clearing it. ANSI-16 uses
	// no background, so there is nothing to re-anchor.
	if bg != "" {
		hl = strings.ReplaceAll(hl, ansiReset, ansiReset+bg)
	}
	return bg + hl
}

// diffBgSGR returns the SGR background for a diff line at the detected color
// depth. ANSI-16 terminals get no tinted background (Rust: fallback returns
// empty backgrounds for ANSI-16) because saturated palette backgrounds
// overpower syntax tokens; the sign and foreground still carry add/delete color.
func diffBgSGR(lineType DiffLineType, light bool, level StdoutColorLevel) string {
	if level == ColorANSI16 {
		return ""
	}
	if light {
		switch lineType {
		case DiffLineInsert:
			if level == ColorANSI256 {
				return "\x1b[48;5;194m"
			}
			return ansiBgAddLight
		case DiffLineDelete:
			if level == ColorANSI256 {
				return "\x1b[48;5;224m"
			}
			return ansiBgDelLight
		}
		return ""
	}
	switch lineType {
	case DiffLineInsert:
		if level == ColorANSI256 {
			return "\x1b[48;5;22m"
		}
		return ansiBgAddDark
	case DiffLineDelete:
		if level == ColorANSI256 {
			return "\x1b[48;5;52m"
		}
		return ansiBgDelDark
	}
	return ""
}

// diffSignSGR returns the SGR prefix for the gutter `+`/`-` sign. Light themes
// and ANSI-16 terminals use a foreground-only sign so the line background (or
// the terminal default) shows through, while dark rich-color levels combine the
// sign color with the tinted line background.
func diffSignSGR(lineType DiffLineType, light bool, level StdoutColorLevel) string {
	fg := ansiGreen
	if lineType == DiffLineDelete {
		fg = ansiRed
	}
	if light || level == ColorANSI16 {
		return fg
	}
	return fg + diffBgSGR(lineType, light, level)
}

func diffFgSGR(lineType DiffLineType, light bool) string {
	if light {
		// GitHub-style pastel backgrounds already contrast with the default
		// foreground, so plain text needs no explicit color.
		return ""
	}
	switch lineType {
	case DiffLineInsert:
		return ansiGreen
	case DiffLineDelete:
		return ansiRed
	}
	return ""
}

// diffThemeIsLight reports whether the active TUI theme is a light palette so
// the diff renderer picks GitHub-style pastel backgrounds (Rust DiffTheme).
func diffThemeIsLight(theme string) bool {
	id := strings.ToLower(strings.TrimSpace(theme))
	if id == "" {
		return false
	}
	if strings.Contains(id, "light") || strings.Contains(id, "latte") || strings.Contains(id, "xcode") || strings.Contains(id, "github-light") {
		return true
	}
	return false
}

// detectLangForPath returns a chroma language token for a diff file path (empty
// when the extension is not a source language), mirroring Rust
// detect_lang_for_path.
func detectLangForPath(path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch ext {
	case "", "txt", "log", "md", "markdown", "http", "php", "html", "htm", "json", "yaml", "yml", "toml", "xml", "svg", "css", "graphql", "sql", "ini", "conf":
		return ""
	}
	// Normalize C-family extensions to chroma's "c" lexer token.
	switch ext {
	case "cc", "cpp", "cxx", "hh", "hpp":
		return "c"
	}
	return ext
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
