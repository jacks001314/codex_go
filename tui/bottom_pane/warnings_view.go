package bottompane

import (
	"fmt"
	"strconv"
	"strings"

	codextui "codex_go/tui"
	"codex_go/tui/history_cell"
	"github.com/mattn/go-runewidth"
)

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

// WarningsHints carries the footer labels the caller resolves from the keymap
// (Rust's render reads the runtime keymap for the same hints). An empty label
// drops that item.
type WarningsHints struct {
	// Keep is the plain `k` label ("k").
	Keep string
	// Cancel is the list cancel hint (dismiss and close).
	Cancel string
	// Copy is the global copy hint, when bound.
	Copy string
	// Navigation is the joined move-left/move-right labels, when bound.
	Navigation string
	// Scroll is the list move-down hint, when bound.
	Scroll string
}

// RenderLines mirrors `warnings_view_render.rs`: a title line naming the current
// diagnostic, a blank separator, the wrapped and scrolled details, and the
// footer hint items. The result has exactly height lines (blank-padded); nil
// means the area is too small to draw.
func (v *WarningsView) RenderLines(width int, height int, hints WarningsHints) []string {
	if v == nil || width < 5 || height < 3 {
		return nil
	}
	inner := width - 4
	title := "Warnings"
	details := "No warnings"
	if entry, ok := v.CurrentEntry(); ok {
		title = fmt.Sprintf("Warnings \u00b7 %d of %d \u00b7 %s", v.current+1, len(v.entries), entry.Source)
		details = entry.Details
	}
	bodyHeight := height - 3
	body := warningsWrapText(details, inner)
	if len(body) > 0 {
		v.MarkVisited()
	}
	v.SetPageMetrics(bodyHeight, len(body))
	offset := v.offset
	rows := make([]string, 0, height)
	rows = append(rows, truncateWarningLine(title, inner), "")
	for index := 0; index < bodyHeight; index++ {
		if offset+index < len(body) {
			line := body[offset+index]
			if codextui.TextContainsURLLike(line) {
				// A web URL is kept whole (never truncated) and annotated as a
				// terminal hyperlink, so the destination stays complete even when
				// it is wider than the panel (Rust #51451).
				rows = append(rows, codextui.AnnotateCompleteWebURLsInLine(line))
			} else {
				rows = append(rows, truncateWarningLine(line, inner))
			}
			continue
		}
		rows = append(rows, "")
	}
	rows = append(rows, warningsFooterLine(hints, inner))
	for len(rows) < height {
		rows = append(rows, "")
	}
	return rows[:height]
}

// warningsFooterLine mirrors Rust's `footer_hint_items_line` item list, dropping
// trailing items that no longer fit the width.
func warningsFooterLine(hints WarningsHints, width int) string {
	items := make([]string, 0, 5)
	if label := strings.TrimSpace(hints.Keep); label != "" {
		items = append(items, label+" keep & next")
	}
	if label := strings.TrimSpace(hints.Cancel); label != "" {
		items = append(items, label+" dismiss & close")
	}
	if label := strings.TrimSpace(hints.Copy); label != "" {
		items = append(items, label+" copy")
	}
	if label := strings.TrimSpace(hints.Navigation); label != "" {
		items = append(items, label+" warning")
	}
	if label := strings.TrimSpace(hints.Scroll); label != "" {
		items = append(items, label+" scroll")
	}
	for len(items) > 0 {
		line := strings.Join(items, " \u00b7 ")
		if runewidth.StringWidth(line) <= width {
			return line
		}
		items = items[:len(items)-1]
	}
	return ""
}

func truncateWarningLine(text string, width int) string {
	if width < 1 {
		return ""
	}
	if runewidth.StringWidth(text) <= width {
		return text
	}
	runes := []rune(text)
	for len(runes) > 0 && runewidth.StringWidth(string(runes)) > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}

// warningsWrapText wraps whitespace-separated words to width, mirroring the
// viewer's plain diagnostics; a word longer than the width occupies its own
// line, except that web URLs stay whole so their complete destination survives
// as a hyperlink (Rust #51451).
func warningsWrapText(text string, width int) []string {
	if width < 1 {
		return nil
	}
	var out []string
	for _, sourceLine := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(sourceLine) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, codextui.WrapLines([]string{sourceLine}, codextui.WrapOptions{Width: width, BreakWords: true})...)
	}
	return out
}

// WarningNotice mirrors `ChatComposer::warning_notice`: the retained-warning
// count with the configured shortcut, degrading from the full sentence to the
// compact form and finally to the bare count.
func WarningNotice(count int, width int, shortcut string) string {
	if count <= 0 || width < 1 {
		return ""
	}
	plural := "s"
	if count == 1 {
		plural = ""
	}
	group := strings.TrimSpace(shortcut)
	if group == "" {
		group = "/warnings"
	}
	full := "\u26a0 " + strconv.Itoa(count) + " warning" + plural + " \u00b7 " + group + " to view"
	if runewidth.StringWidth(full) <= width {
		return full
	}
	compact := "\u26a0 " + strconv.Itoa(count) + " \u00b7 " + group
	if runewidth.StringWidth(compact) <= width {
		return compact
	}
	return truncateWarningLine("\u26a0 "+strconv.Itoa(count), width)
}

// WarningNoticeLine mirrors `warning_notice_layout`: the passive hint row keeps
// its hints and right-aligns the notice when the budget allows, dropping the
// notice entirely before it crowds the hints. left is returned unchanged when no
// notice fits.
func WarningNoticeLine(width int, left string, count int, shortcut string) string {
	if count <= 0 || width <= 0 {
		return left
	}
	available := width - 1
	if available < 1 {
		return left
	}
	budget := available / 2
	if budget < 14 {
		budget = 14
	}
	if budget > available {
		budget = available
	}
	hint := runewidth.StringWidth(left)
	if hint > available {
		hint = available
	}
	if shrink := available - hint - 2; budget > shrink {
		budget = shrink
	}
	if budget < runewidth.StringWidth("\u26a0 "+strconv.Itoa(count)) {
		return left
	}
	notice := WarningNotice(count, budget, shortcut)
	if notice == "" {
		return left
	}
	gap := available - runewidth.StringWidth(left) - runewidth.StringWidth(notice)
	if gap < 0 {
		return left
	}
	return left + strings.Repeat(" ", gap+1) + notice
}
