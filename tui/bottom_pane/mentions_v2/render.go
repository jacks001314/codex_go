package mentionsv2

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// PopupHeight is Rust's fixed mention picker height (MAX_POPUP_ROWS + 9).
const PopupHeight = MaxPopupRows + 9

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiCyan  = "\x1b[36m"
)

var accentSGR = func() string {
	sgr := codextui.SGRForStyle(codextui.StyleSpec{
		Foreground: codextui.AccentColorFor(nil, codextui.ColorTrue),
	}, codextui.ColorTrue)
	if sgr == "" {
		return ansiCyan
	}
	return sgr
}()

// Rust parity subset: codex-rs/tui/src/bottom_pane/mentions_v2/render.rs.

func RenderCandidate(candidate Candidate) string {
	return candidate.DisplayName
}

func RenderPopup(popup *Popup, width int, height int) []string {
	if popup == nil || width <= 0 || height <= 0 {
		return nil
	}
	return renderPopupRows(popup.Rows(), popup.Selected, popup.ScrollTop, popup.FileSearch.EmptyMessage(), popup.SearchMode, popup.Query, width, height)
}

func renderPopupRows(rows []SearchResult, selected int, scrollTop int, emptyMessage string, searchMode SearchMode, query string, width int, height int) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	if height < 4 {
		rendered := renderRowsBlock(rows, selected, scrollTop, width, height, emptyMessage)
		return rendered.lines
	}
	layout := mentionPickerLayout(height - 1)
	out := make([]string, height)
	place := func(offset int, text string) {
		if offset >= 0 && offset < len(out) {
			out[offset] = text
		}
	}
	innerWidth := maxInt(width-4, 0)
	if layout.header >= 1 {
		place(0, "  "+codextui.TruncateToWidth("Mentions", innerWidth))
	}
	if layout.header >= 2 {
		place(1, "  "+codextui.TruncateToWidth("Files, directories, plugins, skills and tasks", innerWidth))
	}
	tabsLine := bottompane.FilledTabBarText(
		[]string{SearchModeResults.Label(), SearchModeFilesystemOnly.Label(), SearchModeTools.Label()},
		searchModeIndex(searchMode),
		innerWidth,
	)
	tabsY := layout.header + layout.headerGap
	if layout.tabs > 0 {
		place(tabsY, "  "+tabsLine)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		query = "Type to search mentions"
	}
	queryY := tabsY + layout.tabs + layout.tabGap
	if layout.search > 0 {
		place(queryY, "  "+codextui.TruncateWithEllipsis(query, innerWidth))
	}
	rendered := renderRowsBlock(rows, selected, scrollTop, width, layout.rows, emptyMessage)
	aboveY := queryY + layout.search
	listY := aboveY + layout.above
	for index := 0; index < layout.rows; index++ {
		line := ""
		if index < len(rendered.lines) {
			line = rendered.lines[index]
		}
		place(listY+index, line)
	}
	if layout.above > 0 && rendered.hasAbove {
		place(aboveY, "\u2191")
	}
	if layout.below > 0 && rendered.hasBelow {
		place(listY+layout.rows, "\u2193")
	}
	footer := RenderFooter(innerWidth, searchMode)
	if footer != "" {
		footer = "  " + footer
	}
	place(height-1, codextui.TruncateToWidth(footer, width))
	return out
}

type mentionPickerLayoutSizes struct {
	header    int
	headerGap int
	tabs      int
	tabGap    int
	search    int
	above     int
	rows      int
	below     int
}

// mentionPickerLayout resolves the fixed mention chrome through the shared
// picker allocator used by Rust's list and mention pickers.
func mentionPickerLayout(areaHeight int) mentionPickerLayoutSizes {
	areas := bottompane.PickerAreas(
		bottompane.PickerRect{Width: 1, Height: areaHeight},
		bottompane.PickerLayoutSizes{Header: 2, HeaderGap: 1, Tabs: 1, Search: 1, Rows: MaxPopupRows},
	)
	return mentionPickerLayoutSizes{
		header:    areas[0].Height,
		headerGap: areas[1].Height,
		tabs:      areas[2].Height,
		tabGap:    areas[3].Height,
		search:    areas[4].Height,
		above:     areas[5].Height,
		rows:      areas[6].Height,
		below:     areas[7].Height,
	}
}

func searchModeIndex(mode SearchMode) int {
	switch mode {
	case SearchModeFilesystemOnly:
		return 1
	case SearchModeTools:
		return 2
	default:
		return 0
	}
}

type renderedRowsBlock struct {
	lines    []string
	hasAbove bool
	hasBelow bool
}

func RenderRows(rows []SearchResult, selected int, scrollTop int, width int, height int, emptyMessage string) []string {
	return renderRowsBlock(rows, selected, scrollTop, width, height, emptyMessage).lines
}

func renderRowsBlock(rows []SearchResult, selected int, scrollTop int, width int, height int, emptyMessage string) renderedRowsBlock {
	if height <= 0 || width <= 0 {
		return renderedRowsBlock{}
	}
	if len(rows) == 0 {
		return renderedRowsBlock{lines: []string{"  " + codextui.TruncateToWidth(emptyMessage, maxInt(width-2, 0))}}
	}
	visible := minInt(MaxPopupRows, minInt(len(rows), height))
	start := scrollTop
	if start < 0 {
		start = 0
	}
	if start > len(rows)-visible {
		start = maxInt(len(rows)-visible, 0)
	}
	if selected >= 0 {
		if selected < start {
			start = selected
		} else if visible > 0 && selected > start+visible-1 {
			start = selected + 1 - visible
		}
	}
	primaryWidth := 0
	for _, row := range rows {
		if rowWidth := primaryTextWidth(row); rowWidth > primaryWidth {
			primaryWidth = rowWidth
		}
	}
	lines := make([]string, 0, visible)
	for index := start; index < minInt(start+visible, len(rows)); index++ {
		lines = append(lines, BuildLine(rows[index], index == selected, width, primaryWidth))
	}
	return renderedRowsBlock{
		lines:    lines,
		hasAbove: start > 0,
		hasBelow: start+visible < len(rows),
	}
}

func BuildLine(row SearchResult, selected bool, width int, primaryColumnWidth int) string {
	if width <= 0 {
		return ""
	}
	gutter := codextui.SelectionPrefix(selected)
	tag := row.MentionType.Tag()
	gutterWidth := codextui.DisplayWidth(gutter)
	tagWidth := codextui.DisplayWidth(tag)
	contentWidth := width - gutterWidth - tagWidth - 2
	if contentWidth < 0 {
		contentWidth = 0
	}
	if maxPrimary := maxInt(contentWidth-2, 0) / 2; primaryColumnWidth > maxPrimary {
		primaryColumnWidth = maxPrimary
	}
	tagText := tag
	content := plainContentText(row, primaryColumnWidth)
	if selected {
		content = codextui.TruncateWithEllipsis(content, contentWidth)
	} else {
		content = codextui.TruncateWithEllipsis(styledContentText(row, primaryColumnWidth), contentWidth)
		tagText = styledTag(row.MentionType, tag)
	}
	padding := width - gutterWidth - ansi.StringWidth(content) - tagWidth
	if padding < 0 {
		padding = 0
	}
	line := gutter + content + strings.Repeat(" ", padding) + tagText
	if selected {
		return codextui.RenderSelectedRowFilled(codextui.TruncateWithEllipsis(line, width), width)
	}
	return codextui.TruncateWithEllipsis(line, width)
}

func plainContentText(row SearchResult, primaryColumnWidth int) string {
	primary := truncateRunes(primaryText(row), primaryColumnWidth)
	secondary := secondaryText(row)
	if secondary == "" {
		return primary
	}
	padding := primaryColumnWidth - codextui.DisplayWidth(primary) + 2
	if padding < 2 {
		padding = 2
	}
	return primary + strings.Repeat(" ", padding) + secondary
}

func styledContentText(row SearchResult, primaryColumnWidth int) string {
	primary := codextui.TruncateWithEllipsis(styledPrimaryText(row), primaryColumnWidth)
	secondary := styledSecondaryText(row)
	if secondary == "" {
		return primary
	}
	padding := primaryColumnWidth - ansi.StringWidth(primary) + 2
	if padding < 2 {
		padding = 2
	}
	return primary + strings.Repeat(" ", padding) + secondary
}

func styledPrimaryText(row SearchResult) string {
	text := primaryText(row)
	offset := 0
	if _, ok := fileName(row); ok {
		offset = fileNameStart(row.DisplayName)
	}
	return applyMatchEmphasis(text, row.MatchIndices, offset, mentionNameSGR(row))
}

func styledSecondaryText(row SearchResult) string {
	if _, ok := fileName(row); ok {
		path := pathPrefix(row)
		if path == "" {
			return ""
		}
		styled := applyMatchEmphasis(path, row.MatchIndices, 0, ansiDim)
		if row.Description != "" {
			return styled + "  " + ansiDim + row.Description + ansiReset
		}
		return styled
	}
	if row.Description == "" {
		return ""
	}
	return ansiDim + row.Description + ansiReset
}

func mentionNameSGR(row SearchResult) string {
	switch row.MentionType {
	case MentionTypePlugin:
		return accentSGR
	case MentionTypeSkill:
		return ansiDim
	case MentionTypeTask, MentionTypeFile:
		return ansiCyan
	default:
		return ""
	}
}

func styledTag(mentionType MentionType, tag string) string {
	style := ""
	switch mentionType {
	case MentionTypePlugin:
		style = accentSGR
	case MentionTypeSkill:
		style = ansiDim
	case MentionTypeTask, MentionTypeFile:
		style = ansiCyan
	}
	if style == "" {
		return tag
	}
	return style + tag + ansiReset
}

func applyMatchEmphasis(text string, indices []int, offset int, baseSGR string) string {
	if text == "" {
		return ""
	}
	runes := []rune(text)
	matched := make(map[int]bool, len(indices))
	for _, index := range indices {
		relative := index - offset
		if relative >= 0 && relative < len(runes) {
			matched[relative] = true
		}
	}
	if len(matched) == 0 && baseSGR == "" {
		return text
	}
	var out strings.Builder
	current := ""
	closeStyle := func() {
		if current != "" {
			out.WriteString(ansiReset)
			current = ""
		}
	}
	for index, r := range runes {
		style := baseSGR
		if matched[index] {
			style += ansiBold
		}
		if style != current {
			closeStyle()
			if style != "" {
				out.WriteString(style)
				current = style
			}
		}
		out.WriteRune(r)
	}
	closeStyle()
	return out.String()
}

func primaryText(row SearchResult) string {
	if fileName, ok := fileName(row); ok {
		return fileName
	}
	return row.DisplayName
}

func secondaryText(row SearchResult) string {
	if _, ok := fileName(row); ok {
		path := pathPrefix(row)
		if path == "" {
			path = "./"
		}
		if row.Description != "" {
			return path + "  " + row.Description
		}
		return path
	}
	return row.Description
}

func primaryTextWidth(row SearchResult) int {
	return codextui.DisplayWidth(primaryText(row))
}

func fileName(row SearchResult) (string, bool) {
	if row.Selection.Kind != SelectionFile || !row.MentionType.IsFilesystem() {
		return "", false
	}
	path := row.DisplayName
	start := fileNameStart(path)
	if start == 0 {
		return path, true
	}
	return path[start:], true
}

func pathPrefix(row SearchResult) string {
	path := row.DisplayName
	if row.Selection.Kind != SelectionFile || !row.MentionType.IsFilesystem() {
		return ""
	}
	start := fileNameStart(path)
	if start == 0 {
		return ""
	}
	return path[:start]
}

func fileNameStart(path string) int {
	idx := strings.LastIndexAny(path, "/\\")
	if idx < 0 {
		return 0
	}
	return idx + 1
}

func truncateRunes(value string, width int) string {
	return codextui.TruncateWithEllipsis(value, width)
}
