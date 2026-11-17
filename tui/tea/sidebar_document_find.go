package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust parity: codex-rs/tui/src/transcript_view/search.rs as the sidebar
// document reader drives it (#61889). The preview is a flat list of rendered
// rows, so Find scans those rows literally, keeps the query caret at the end,
// and shows the reader's own status line in the footer while it is active.

// sidebarDocumentFind is one preview's Find state (Rust TranscriptView::search).
type sidebarDocumentFind struct {
	editing   bool
	query     string
	truncated bool
	// matches are rendered body rows that contain the query.
	matches   []int
	index     int
	exhausted bool
}

func (f *sidebarDocumentFind) active() bool {
	return f != nil && (f.editing || f.query != "")
}

func (f *sidebarDocumentFind) selected() (int, bool) {
	if f == nil || f.index < 0 || f.index >= len(f.matches) {
		return 0, false
	}
	return f.matches[f.index], true
}

// statusHints mirrors Rust Search::status_line's (status, compact) pair.
func (f *sidebarDocumentFind) statusHints() (string, string) {
	switch {
	case f == nil || f.query == "":
		return "Type to find", "Type to find"
	case len(f.matches) == 0:
		return "No matches", "No matches"
	case f.editing && f.exhausted:
		return "No more matches · enter accept · ctrl+p older · ctrl+n newer", "enter accept"
	case f.editing:
		return "enter accept · ctrl+p older · ctrl+n newer", "enter accept"
	case f.exhausted:
		return "No more matches · ctrl+p older · ctrl+n newer", "ctrl+p older"
	default:
		return "ctrl+p older · ctrl+n newer", "ctrl+p older"
	}
}

// barLine is the footer the pane paints while Find owns the reader. Rust's
// first_fitting_line drops hints from the right before clipping the line.
func (f *sidebarDocumentFind) barLine(width int) string {
	if f == nil || !f.active() {
		return ""
	}
	status, compact := f.statusHints()
	limit := ""
	if f.truncated {
		limit = " · query limited to 4 KiB"
	}
	var candidates []string
	if f.editing {
		prefix := "Find: " + f.query
		candidates = []string{
			prefix + " · " + status + " · esc cancel" + limit,
			prefix + " · " + compact + " · esc cancel",
			prefix + " · esc cancel",
		}
	} else {
		candidates = []string{
			"Find · " + status + " · esc latest",
			"Find · " + compact + " · esc latest",
			"esc latest",
		}
	}
	for _, candidate := range candidates {
		if width <= 0 || codextui.DisplayWidth(candidate) <= width {
			return candidate
		}
	}
	return codextui.TruncateToWidth(candidates[len(candidates)-1], max(width, 1))
}

// beginSidebarDocumentFind opens the query editor. Reopening while a query is
// accepted returns to editing without dropping the query or its match.
func (m *Model) beginSidebarDocumentFind(tab *sidebarDocumentTab) {
	if m == nil || tab == nil {
		return
	}
	tab.Find.editing = true
	tab.Find.exhausted = false
	tab.Selection.clear()
	if tab.Find.query != "" {
		m.refreshSidebarDocumentFindMatches(tab, false)
	}
	m.refreshTranscript()
}

func (m *Model) cancelSidebarDocumentFind(tab *sidebarDocumentTab) {
	if m == nil || tab == nil {
		return
	}
	tab.Find = sidebarDocumentFind{}
	m.refreshTranscript()
}

// refreshSidebarDocumentFindMatches rescans the rendered rows. resetSelection
// starts from the first match at or below the viewport top, which is where a
// reader typing a query expects the search to begin.
func (m *Model) refreshSidebarDocumentFindMatches(tab *sidebarDocumentTab, resetSelection bool) {
	if m == nil || tab == nil {
		return
	}
	f := &tab.Find
	f.matches = nil
	if f.query == "" {
		f.index = -1
		return
	}
	folded := strings.ToLower(f.query)
	for row, line := range m.sidebarDocumentLines(tab, m.sidebarDocumentBodyWidth()) {
		if strings.Contains(strings.ToLower(ansi.Strip(line)), folded) {
			f.matches = append(f.matches, row)
		}
	}
	if len(f.matches) == 0 {
		f.index = -1
		return
	}
	if resetSelection || f.index < 0 || f.index >= len(f.matches) {
		f.index = 0
		for index, row := range f.matches {
			if row >= tab.Scroll {
				f.index = index
				break
			}
		}
	}
	m.sidebarDocumentRevealRow(tab, f.matches[f.index])
}

// stepSidebarDocumentFind moves to the older or newer match without wrapping,
// marking the ends exhausted the way the reader does.
func (m *Model) stepSidebarDocumentFind(tab *sidebarDocumentTab, delta int) bool {
	if m == nil || tab == nil || len(tab.Find.matches) == 0 {
		return false
	}
	next := tab.Find.index + delta
	if next < 0 || next >= len(tab.Find.matches) {
		tab.Find.exhausted = true
		m.refreshTranscript()
		return false
	}
	tab.Find.index = next
	tab.Find.exhausted = false
	m.sidebarDocumentRevealRow(tab, tab.Find.matches[next])
	m.refreshTranscript()
	return true
}

// updateSidebarDocumentFindKey owns every key while Find is open, so links,
// paging, and Esc-close stay untouched until the search ends.
func (m *Model) updateSidebarDocumentFindKey(tab *sidebarDocumentTab, msg keymsg.KeyMsg) (bool, bubbletea.Cmd) {
	if m == nil || tab == nil || !tab.Find.active() {
		return false, nil
	}
	f := &tab.Find
	switch msg.Type {
	case keymsg.KeyEsc:
		m.cancelSidebarDocumentFind(tab)
		return true, nil
	case keymsg.KeyCtrlC:
		m.cancelSidebarDocumentFind(tab)
		return true, nil
	case keymsg.KeyEnter:
		if f.query != "" && len(f.matches) > 0 {
			f.editing = false
			f.exhausted = false
			m.refreshTranscript()
		}
		return true, nil
	case keymsg.KeyCtrlP:
		if !f.editing {
			m.stepSidebarDocumentFind(tab, -1)
		}
		return true, nil
	case keymsg.KeyCtrlN:
		if !f.editing {
			m.stepSidebarDocumentFind(tab, 1)
		}
		return true, nil
	}
	if !f.editing {
		if m.keyMatches("global", "find_transcript", keySpecFromKeyMsg(msg)) ||
			m.keyMatches("pager", "find", keySpecFromKeyMsg(msg)) {
			m.beginSidebarDocumentFind(tab)
		}
		return true, nil
	}
	switch msg.Type {
	case keymsg.KeyBackspace:
		if f.query == "" {
			return true, nil
		}
		runes := []rune(f.query)
		f.query = string(runes[:len(runes)-1])
		f.truncated = false
	case keymsg.KeyRunes, keymsg.KeySpace:
		text := string(msg.Runes)
		if msg.Type == keymsg.KeySpace {
			text = " "
		}
		if text == "" {
			return true, nil
		}
		if len(f.query)+len(text) > mainFindQueryBytes {
			f.truncated = true
			return true, nil
		}
		f.query += text
		f.exhausted = false
	default:
		return true, nil
	}
	m.refreshSidebarDocumentFindMatches(tab, true)
	m.refreshTranscript()
	return true, nil
}

// renderSidebarDocumentFindTab paints the status footer while Find is active,
// or the reader hints otherwise.
func (m *Model) sidebarDocumentFooterLine(width int, tab *sidebarDocumentTab) string {
	if tab != nil && tab.Find.active() {
		return tab.Find.barLine(width)
	}
	return m.sidebarDocumentFooter(width, tab)
}
