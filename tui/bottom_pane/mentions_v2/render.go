package mentionsv2

import (
	"strings"

	codextui "codex_go/tui"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/mentions_v2/render.rs.

func RenderCandidate(candidate Candidate) string {
	return candidate.DisplayName
}

func RenderPopup(popup *Popup, width int, height int) []string {
	if popup == nil {
		return nil
	}
	listHeight := height
	withFooter := height > 2
	if withFooter {
		listHeight = height - 2
	}
	rows := RenderRows(popup.Rows(), popup.Selected, popup.ScrollTop, width, listHeight, popup.FileSearch.EmptyMessage())
	if withFooter {
		rows = append(rows, "")
		rows = append(rows, RenderFooter(width-2, popup.SearchMode))
	}
	return rows
}

func RenderRows(rows []SearchResult, selected int, scrollTop int, width int, height int, emptyMessage string) []string {
	if height <= 0 {
		return nil
	}
	if len(rows) == 0 {
		return []string{"  " + codextui.TruncateToWidth(emptyMessage, maxInt(width-2, 0))}
	}
	visible := minInt(MaxPopupRows, minInt(len(rows), height))
	start := scrollTop
	if start < 0 {
		start = 0
	}
	if start > len(rows)-1 {
		start = len(rows) - 1
	}
	if selected >= 0 {
		if selected < start {
			start = selected
		} else if visible > 0 && selected > start+visible-1 {
			start = selected + 1 - visible
		}
	}
	primaryWidth := 0
	for _, row := range rows[start:minInt(start+visible, len(rows))] {
		if width := primaryTextWidth(row); width > primaryWidth {
			primaryWidth = width
		}
	}
	out := []string{}
	for idx := start; idx < minInt(start+visible, len(rows)); idx++ {
		line := BuildLine(rows[idx], idx == selected, width, primaryWidth)
		out = append(out, line)
	}
	return out
}

func BuildLine(row SearchResult, selected bool, width int, primaryColumnWidth int) string {
	if width <= 0 {
		return ""
	}
	gutter := "  "
	if selected {
		gutter = "> "
	}
	tag := row.MentionType.Tag()
	gutterWidth := codextui.DisplayWidth(gutter)
	tagWidth := codextui.DisplayWidth(tag)
	contentWidth := width - gutterWidth - tagWidth - 2
	if contentWidth < 0 {
		contentWidth = 0
	}
	content := contentText(row, primaryColumnWidth)
	content = truncateRunes(content, contentWidth)
	padding := width - gutterWidth - codextui.DisplayWidth(content) - tagWidth
	if padding < 0 {
		padding = 0
	}
	line := gutter + content + strings.Repeat(" ", padding) + tag
	if selected {
		return codextui.RenderSelectedRow(truncateRunes(line, width))
	}
	return truncateRunes(line, width)
}

func contentText(row SearchResult, primaryColumnWidth int) string {
	primary := primaryText(row)
	secondary := secondaryText(row)
	if secondary == "" {
		return primary
	}
	padding := primaryColumnWidth - primaryTextWidth(row) + 2
	if padding < 2 {
		padding = 2
	}
	return primary + strings.Repeat(" ", padding) + secondary
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
	return len([]rune(primaryText(row)))
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
