package mentionsv2

import "codex_go/filesearch"

// Rust parity subset: codex-rs/tui/src/bottom_pane/mentions_v2/popup.rs.

const MaxPopupRows = 8

type Popup struct {
	Query      string
	FileSearch FileSearch
	Candidates []Candidate
	SearchMode SearchMode
	Selected   int
	ScrollTop  int
	rows       []SearchResult
}

func NewPopup(candidates []Candidate) *Popup {
	return &Popup{
		Candidates: append([]Candidate(nil), candidates...),
		SearchMode: SearchModeResults,
		Selected:   -1,
	}
}

func NewPopupWithQuery(candidates []Candidate, query string) *Popup {
	popup := NewPopup(candidates)
	popup.Query = query
	popup.FileSearch.SetQuery(query)
	popup.refreshRows()
	return popup
}

func (p *Popup) SetCandidates(candidates []Candidate) {
	if p == nil {
		return
	}
	p.Candidates = append([]Candidate(nil), candidates...)
	p.refreshRows()
}

func (p *Popup) SetQuery(query string) {
	if p == nil {
		return
	}
	if p.Query == query && p.rows != nil {
		return
	}
	p.Query = query
	p.FileSearch.SetQuery(query)
	p.refreshRows()
}

func (p *Popup) SetFileMatches(query string, matches []filesearch.FileMatch) {
	if p == nil {
		return
	}
	p.FileSearch.SetMatches(query, matches)
	p.refreshRows()
}

func (p *Popup) SelectedSelection() (Selection, bool) {
	if p == nil {
		return Selection{}, false
	}
	if p.Selected < 0 || p.Selected >= len(p.rows) {
		return Selection{}, false
	}
	return p.rows[p.Selected].Selection, true
}

func (p *Popup) MoveUp() {
	if p == nil {
		return
	}
	length := len(p.rows)
	if length == 0 {
		p.Selected = -1
		p.ScrollTop = 0
		return
	}
	if p.Selected < 0 {
		p.Selected = 0
	} else if p.Selected == 0 {
		p.Selected = length - 1
	} else {
		p.Selected--
	}
	p.EnsureVisible(length)
}

func (p *Popup) MoveDown() {
	if p == nil {
		return
	}
	length := len(p.rows)
	if length == 0 {
		p.Selected = -1
		p.ScrollTop = 0
		return
	}
	if p.Selected < 0 || p.Selected >= length-1 {
		p.Selected = 0
	} else {
		p.Selected++
	}
	p.EnsureVisible(length)
}

func (p *Popup) PreviousSearchMode() {
	if p == nil {
		return
	}
	p.SearchMode = p.SearchMode.Previous()
	p.refreshRows()
}

func (p *Popup) NextSearchMode() {
	if p == nil {
		return
	}
	p.SearchMode = p.SearchMode.Next()
	p.refreshRows()
}

func (p *Popup) CalculateRequiredHeight(width int) int {
	visible := minInt(MaxPopupRows, maxInt(1, len(p.rows)))
	return visible + 2
}

func (p *Popup) Rows() []SearchResult {
	if p == nil {
		return nil
	}
	return append([]SearchResult(nil), p.rows...)
}

func (p *Popup) ClampSelection() {
	if p == nil {
		return
	}
	p.refreshRows()
}

func (p *Popup) refreshRows() {
	p.rows = FilteredCandidates(p.Candidates, p.FileSearch.Matches, p.Query, p.SearchMode, p.FileSearch.ShouldShowMatches())
	length := len(p.rows)
	if length == 0 {
		p.Selected = -1
		p.ScrollTop = 0
		return
	}
	if p.Selected < 0 {
		p.Selected = 0
	} else if p.Selected >= length {
		p.Selected = length - 1
	}
	p.EnsureVisible(length)
}

func (p *Popup) EnsureVisible(length int) {
	if p == nil || length <= 0 {
		return
	}
	visible := minInt(MaxPopupRows, length)
	if p.ScrollTop > length-1 {
		p.ScrollTop = maxInt(length-1, 0)
	}
	if p.Selected < p.ScrollTop {
		p.ScrollTop = p.Selected
	}
	if p.Selected >= p.ScrollTop+visible {
		p.ScrollTop = p.Selected + 1 - visible
	}
	if p.ScrollTop < 0 {
		p.ScrollTop = 0
	}
}

type FileSearch struct {
	PendingQuery string
	DisplayQuery string
	Waiting      bool
	Matches      []filesearch.FileMatch
}

func (s *FileSearch) SetQuery(query string) {
	if s == nil {
		return
	}
	if query == "" {
		s.PendingQuery = ""
		s.DisplayQuery = ""
		s.Waiting = false
		s.Matches = nil
		return
	}
	if query != s.PendingQuery {
		s.PendingQuery = query
		s.Waiting = true
	}
}

func (s *FileSearch) SetMatches(query string, matches []filesearch.FileMatch) {
	if s == nil || query != s.PendingQuery {
		return
	}
	s.DisplayQuery = query
	s.Matches = append([]filesearch.FileMatch(nil), matches...)
	if len(s.Matches) > MaxPopupRows {
		s.Matches = s.Matches[:MaxPopupRows]
	}
	s.Waiting = false
}

func (s *FileSearch) ShouldShowMatches() bool {
	return s != nil && len(s.Matches) > 0
}

func (s *FileSearch) EmptyMessage() string {
	if s != nil && s.Waiting {
		return "loading..."
	}
	return "no matches"
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
