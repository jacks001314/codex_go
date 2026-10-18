package bottompane

import (
	"path/filepath"
	"strings"

	"codex_go/filesearch"
	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/bottom_pane/file_search_popup.rs.

type FileSearchPopupResult struct {
	Path string
}

type FileSearchMatch struct {
	Path    string
	Root    string
	Score   int
	Indices []int
}

type FileSearchPopup struct {
	DisplayQuery string
	PendingQuery string
	Waiting      bool
	Matches      []FileSearchMatch
	State        ScrollState
}

func NewFileSearchPopup() *FileSearchPopup {
	return &FileSearchPopup{Waiting: true, State: NewScrollState()}
}

func (p *FileSearchPopup) SetQuery(query string) {
	if p == nil || query == p.PendingQuery {
		return
	}
	p.PendingQuery = query
	p.Waiting = true
}

func (p *FileSearchPopup) SetEmptyPrompt() {
	if p == nil {
		return
	}
	p.DisplayQuery = ""
	p.PendingQuery = ""
	p.Waiting = false
	p.Matches = nil
	p.State.Reset()
}

func (p *FileSearchPopup) SetMatches(query string, matches []FileSearchMatch) {
	if p == nil || query != p.PendingQuery {
		return
	}
	p.DisplayQuery = query
	p.Matches = append([]FileSearchMatch(nil), matches...)
	if len(p.Matches) > MaxPopupRows {
		p.Matches = p.Matches[:MaxPopupRows]
	}
	p.Waiting = false
	length := len(p.Matches)
	p.State.ClampSelection(length)
	p.State.EnsureVisible(length, min(MaxPopupRows, length))
}

func (p *FileSearchPopup) MoveUp() {
	if p == nil {
		return
	}
	length := len(p.Matches)
	p.State.MoveUpWrap(length)
	p.State.EnsureVisible(length, min(MaxPopupRows, length))
}

func (p *FileSearchPopup) MoveDown() {
	if p == nil {
		return
	}
	length := len(p.Matches)
	p.State.MoveDownWrap(length)
	p.State.EnsureVisible(length, min(MaxPopupRows, length))
}

func (p *FileSearchPopup) SelectedMatch() (FileSearchMatch, bool) {
	if p == nil || !p.State.HasSelection || p.State.SelectedIdx < 0 || p.State.SelectedIdx >= len(p.Matches) {
		return FileSearchMatch{}, false
	}
	return p.Matches[p.State.SelectedIdx], true
}

func (p *FileSearchPopup) CalculateRequiredHeight() int {
	if p == nil {
		return 0
	}
	return min(max(len(p.Matches), 1), MaxPopupRows) + 2
}

func (p *FileSearchPopup) Rows(width int) []string {
	if p == nil {
		return nil
	}
	if len(p.Matches) == 0 {
		if p.Waiting {
			return []string{"  loading..."}
		}
		return []string{"  no matches"}
	}
	length := len(p.Matches)
	p.State.ClampSelection(length)
	p.State.EnsureVisible(length, min(MaxPopupRows, length))
	start := p.State.ScrollTop
	end := min(start+MaxPopupRows, length)
	rows := make([]string, 0, end-start)
	nameWidth := max(width-2, 1)
	for idx := start; idx < end; idx++ {
		match := p.Matches[idx]
		name := fileSearchDisplayPath(match.Path, nameWidth)
		indices := match.Indices
		if name != match.Path {
			if fuzzy, ok := filesearch.FuzzyMatch(name, p.DisplayQuery); ok {
				indices = fuzzy.Indices
			}
		}
		selected := p.State.HasSelection && idx == p.State.SelectedIdx
		text := name
		if !selected {
			text = highlightFileSearchMatch(name, indices)
		}
		row := tui.SelectionPrefix(selected) + text
		if selected {
			row = tui.RenderSelectedRowFilled(row, width)
		}
		rows = append(rows, row)
	}
	return rows
}

// fileSearchDisplayPath keeps the filename visible when a long parent path does
// not fit, mirroring Rust center_truncate_path/parent shortening (#46694).
func fileSearchDisplayPath(path string, width int) string {
	if width <= 0 || tui.DisplayWidth(path) <= width {
		return path
	}
	filename := filepath.Base(path)
	if filename == "." || filename == string(filepath.Separator) {
		filename = path
	}
	filenameWidth := tui.DisplayWidth(filename)
	if filenameWidth+2 > width {
		return tui.CenterTruncatePath(filename, width)
	}
	parent := filepath.Dir(path)
	if parent == "." || parent == "" {
		return filename
	}
	parent = tui.TruncateWithEllipsis(parent, width-filenameWidth-1)
	separator := string(filepath.Separator)
	if strings.HasSuffix(parent, separator) {
		return parent + filename
	}
	return parent + separator + filename
}

func highlightFileSearchMatch(text string, indices []int) string {
	if text == "" || len(indices) == 0 {
		return text
	}
	matched := make(map[int]bool, len(indices))
	for _, index := range indices {
		matched[index] = true
	}
	var out strings.Builder
	bold := false
	for index, r := range []rune(text) {
		if matched[index] && !bold {
			out.WriteString("\x1b[1m")
			bold = true
		} else if !matched[index] && bold {
			out.WriteString("\x1b[22m")
			bold = false
		}
		out.WriteRune(r)
	}
	if bold {
		out.WriteString("\x1b[22m")
	}
	return out.String()
}
