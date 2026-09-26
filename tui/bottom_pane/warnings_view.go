package bottompane

import "codex_go/tui/history_cell"

// Rust parity: codex-rs/tui/src/bottom_pane/warnings_view.rs. A frozen set of
// warnings shown one at a time without changing the retained draft; on
// intentional close only the warnings actually drawn are dismissed, unless the
// user kept them. This owns the navigation/dismissal state; key mapping and
// rendering live with the callers.

// WarningsView is the warnings panel's navigation and dismissal state.
type WarningsView struct {
	entries   []historycell.WarningEntry
	current   int
	visited   []bool
	kept      map[int]bool
	offset    int
	pageSize  int
	maxOffset int
}

// NewWarningsView mirrors `WarningsView::new`: the entries are frozen and every
// page starts unvisited and unkept.
func NewWarningsView(entries []historycell.WarningEntry) *WarningsView {
	return &WarningsView{
		entries: append([]historycell.WarningEntry(nil), entries...),
		visited: make([]bool, len(entries)),
		kept:    map[int]bool{},
	}
}

// Entries returns the frozen diagnostics.
func (v *WarningsView) Entries() []historycell.WarningEntry {
	if v == nil {
		return nil
	}
	return append([]historycell.WarningEntry(nil), v.entries...)
}

// CurrentIndex is the page being shown.
func (v *WarningsView) CurrentIndex() int {
	if v == nil {
		return 0
	}
	return v.current
}

// CurrentEntry returns the page being shown.
func (v *WarningsView) CurrentEntry() (historycell.WarningEntry, bool) {
	if v == nil || v.current < 0 || v.current >= len(v.entries) {
		return historycell.WarningEntry{}, false
	}
	return v.entries[v.current], true
}

// Offset is the current scroll offset within the page.
func (v *WarningsView) Offset() int {
	if v == nil {
		return 0
	}
	return v.offset
}

// MaxOffset is the largest valid offset for the last rendered page.
func (v *WarningsView) MaxOffset() int {
	if v == nil {
		return 0
	}
	return v.maxOffset
}

// MarkVisited records that the current page was drawn (Rust sets the render cell
// only when the body area is non-empty).
func (v *WarningsView) MarkVisited() {
	if v == nil || v.current < 0 || v.current >= len(v.visited) {
		return
	}
	v.visited[v.current] = true
}

// SetPageMetrics records the rendered page size and clamps the live offset,
// mirroring the render pass's `page_size`/`max_offset` bookkeeping.
func (v *WarningsView) SetPageMetrics(pageSize int, lineCount int) {
	if v == nil {
		return
	}
	if pageSize < 1 {
		pageSize = 1
	}
	v.pageSize = pageSize
	v.maxOffset = lineCount - pageSize
	if v.maxOffset < 0 {
		v.maxOffset = 0
	}
	if v.offset > v.maxOffset {
		v.offset = v.maxOffset
	}
}

// MoveLeft mirrors `ListAction::MoveLeft`: the previous page, resetting the
// scroll offset only when the page changes.
func (v *WarningsView) MoveLeft() {
	if v == nil {
		return
	}
	v.moveTo(v.current - 1)
}

// MoveRight mirrors `ListAction::MoveRight`: the next page, clamped.
func (v *WarningsView) MoveRight() {
	if v == nil {
		return
	}
	v.moveTo(v.current + 1)
}

func (v *WarningsView) moveTo(index int) {
	last := len(v.entries) - 1
	if index > last {
		index = last
	}
	if index < 0 {
		index = 0
	}
	if index != v.current {
		v.current = index
		v.offset = 0
	}
}

// MoveUp scrolls the current page up one line.
func (v *WarningsView) MoveUp() {
	if v == nil {
		return
	}
	if v.offset > 0 {
		v.offset--
	}
}

// MoveDown scrolls the current page down one line, clamped to the rendered page.
func (v *WarningsView) MoveDown() {
	if v == nil {
		return
	}
	if v.offset+1 <= v.maxOffset {
		v.offset++
	}
}

// PageUp mirrors `ListAction::PageUp`.
func (v *WarningsView) PageUp() {
	if v == nil {
		return
	}
	v.offset -= v.pageSizeOrOne()
	if v.offset < 0 {
		v.offset = 0
	}
}

// PageDown mirrors `ListAction::PageDown`.
func (v *WarningsView) PageDown() {
	if v == nil {
		return
	}
	v.offset += v.pageSizeOrOne()
	if v.offset > v.maxOffset {
		v.offset = v.maxOffset
	}
}

// JumpTop mirrors `ListAction::JumpTop`.
func (v *WarningsView) JumpTop() {
	if v == nil {
		return
	}
	v.offset = 0
}

// JumpBottom mirrors `ListAction::JumpBottom`.
func (v *WarningsView) JumpBottom() {
	if v == nil {
		return
	}
	v.offset = v.maxOffset
}

// KeepAndNext mirrors the viewer's plain `k`: the current page is kept, and the
// returned bool reports that the viewer should close because it was the last
// page. Otherwise the viewer advances one page.
func (v *WarningsView) KeepAndNext() bool {
	if v == nil {
		return true
	}
	v.kept[v.current] = true
	if v.current+1 >= len(v.entries) {
		return true
	}
	v.MoveRight()
	return false
}

// Close mirrors `WarningsView::close`: an unkept page is dismissed only when it
// was actually drawn, and the returned slices keep the entries' original order.
func (v *WarningsView) Close() (dismissed []historycell.WarningEntry, kept []historycell.WarningEntry) {
	if v == nil {
		return nil, nil
	}
	for index, entry := range v.entries {
		switch {
		case v.kept[index]:
			kept = append(kept, entry)
		case index < len(v.visited) && v.visited[index]:
			dismissed = append(dismissed, entry)
		}
	}
	return dismissed, kept
}

func (v *WarningsView) pageSizeOrOne() int {
	if v == nil || v.pageSize < 1 {
		return 1
	}
	return v.pageSize
}
