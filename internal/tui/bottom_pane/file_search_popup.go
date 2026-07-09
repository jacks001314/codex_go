package bottompane

import "codex_go/internal/tui"

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
	if len(p.Matches) == 0 {
		return 1
	}
	return min(MaxPopupRows, len(p.Matches))
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
	for idx := start; idx < end; idx++ {
		wrapped := tui.AdaptiveWrapLine(p.Matches[idx].Path, tui.WrapOptions{
			Width:      max(width-2, 1),
			BreakWords: true,
		})
		for _, part := range wrapped {
			row := "  " + part
			if p.State.HasSelection && idx == p.State.SelectedIdx {
				row = tui.RenderSelectedRow(row)
			}
			rows = append(rows, row)
		}
	}
	return rows
}
