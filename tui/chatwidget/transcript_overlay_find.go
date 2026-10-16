package chatwidget

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// Rust parity: codex-rs/tui/src/transcript_view/search.rs (find-bar state and
// key handling), search_footer.rs ("Find: " editor row plus Search::status_line),
// and the `pager.find` keymap action (keymap_setup/actions.rs:256, bound to `f3`
// and `/` by keymap.rs:1929 and consumed in app_backtrack/legacy_input.rs:34).
//
// Rust keeps the query in a full TextArea editor with a caret and walks history
// cells through EntryKey anchors; this pager holds the transcript as a flat line
// buffer, so the query is a plain string and matches are content line indices.
// update/r90tui_2026_10_16.md §T13 lists the parts that stay unlanded.
const (
	// Rust transcript_view/search.rs QUERY_BYTES: input past 4 KiB is refused and
	// the status line reports the truncated query.
	findQueryBytes = 4096
	// Rust search_footer.rs renders the editor row as "Find: " plus the query.
	findFooterPrefix = "Find: "
)

// findProgress mirrors the states of Rust transcript_view::search::Progress that
// this pager can reach without an asynchronous history scan.
type findProgress int

const (
	findProgressIdle findProgress = iota
	findProgressFound
	findProgressExhausted
)

// transcriptFind is the transcript pager's find-bar state.
type transcriptFind struct {
	// editing is Rust SearchMode::Editing: the query editor owns the keyboard.
	editing bool
	// accepted is Rust SearchMode::Reading: a match is on screen, the pager owns
	// the keyboard again, and Esc returns to the latest output.
	accepted  bool
	query     string
	truncated bool
	// matches are the content line indices whose text contains the folded query.
	matches  []int
	index    int
	progress findProgress
}

func (f *transcriptFind) active() bool { return f.editing || f.accepted }

func (f *transcriptFind) matchLine() (int, bool) {
	if f.index < 0 || f.index >= len(f.matches) {
		return 0, false
	}
	return f.matches[f.index], true
}

// FindActive reports whether the find bar owns the pager keyboard.
func (o *TranscriptOverlay) FindActive() bool {
	return o != nil && o.find.active()
}

// FindEditing reports whether the query editor still owns typing.
func (o *TranscriptOverlay) FindEditing() bool {
	return o != nil && o.find.editing
}

// FindQuery returns the current query text.
func (o *TranscriptOverlay) FindQuery() string {
	if o == nil {
		return ""
	}
	return o.find.query
}

// FindTruncated reports whether input was refused at the 4 KiB cap.
func (o *TranscriptOverlay) FindTruncated() bool {
	return o != nil && o.find.truncated
}

// FindMatchCount returns how many content lines match the query.
func (o *TranscriptOverlay) FindMatchCount() int {
	if o == nil {
		return 0
	}
	return len(o.find.matches)
}

// FindMatchLine reports the content line of the displayed match.
func (o *TranscriptOverlay) FindMatchLine() (int, bool) {
	if o == nil {
		return 0, false
	}
	return o.find.matchLine()
}

// FindStatusLine mirrors Rust Search::status_line for the states this pager can
// reach: empty query, a live query, a found match, and exhaustion with or
// without a hit.
func (o *TranscriptOverlay) FindStatusLine() string {
	if o == nil {
		return ""
	}
	switch {
	case o.find.query == "":
		return "Type to find"
	case len(o.find.matches) == 0:
		return "No matches"
	case o.find.progress == findProgressExhausted:
		return "No more matches · enter accept · ctrl+p older · ctrl+n newer"
	default:
		return "enter accept · ctrl+p older · ctrl+n newer"
	}
}

// FindBarLine is the footer row the pager shows while the find bar is active.
// Rust renders "Find: " plus the query editor and then Search::status_line; in
// reading mode it renders "Find · <status> · esc latest".
func (o *TranscriptOverlay) FindBarLine() string {
	if o == nil || !o.find.active() {
		return ""
	}
	status := o.FindStatusLine()
	if !o.find.editing {
		return "Find · " + status + " · esc latest"
	}
	line := findFooterPrefix + o.find.query + " · " + status + " · esc cancel"
	if o.find.truncated {
		line += " · query limited to 4 KiB"
	}
	return line
}

// BeginFind mirrors Rust TranscriptView::begin_search: the find bar takes the
// keyboard. A second `pager.find` press while editing is a no-op so the query is
// never cleared by a repeated trigger.
func (o *TranscriptOverlay) BeginFind() {
	if o == nil || o.find.editing {
		return
	}
	o.find.editing = true
	o.find.accepted = false
	o.refreshFindMatches()
}

// CancelFind mirrors Rust Search::cancel_search: the query and its matches are
// dropped and the pager shows the plain transcript again.
func (o *TranscriptOverlay) CancelFind() {
	if o == nil {
		return
	}
	o.find = transcriptFind{index: -1}
	o.refreshFindContent()
}

// AcceptFind mirrors the Rust Enter branch: the displayed match stays on screen,
// the pager takes the keyboard back, and Esc returns to the latest output. An
// empty query or a query without matches leaves the editor open.
func (o *TranscriptOverlay) AcceptFind() bool {
	if o == nil || !o.find.editing {
		return false
	}
	if o.find.query == "" || len(o.find.matches) == 0 {
		return false
	}
	o.find.editing = false
	o.find.accepted = true
	o.refreshFindContent()
	return true
}

// FindNext walks to the newer match (Rust ctrl+n).
func (o *TranscriptOverlay) FindNext() bool { return o.stepFind(1) }

// FindPrevious walks to the older match (Rust ctrl+p).
func (o *TranscriptOverlay) FindPrevious() bool { return o.stepFind(-1) }

// stepFind mirrors Rust next_search_match: matches never wrap, and running past
// either end reports "No more matches" while the current match stays visible.
func (o *TranscriptOverlay) stepFind(delta int) bool {
	if o == nil || !o.find.active() || len(o.find.matches) == 0 {
		return false
	}
	next := o.find.index + delta
	if next < 0 || next >= len(o.find.matches) {
		o.find.progress = findProgressExhausted
		return false
	}
	o.find.index = next
	o.find.progress = findProgressFound
	o.scrollToFindMatch()
	o.refreshFindContent()
	return true
}

// HandleFindKey routes one key to the find bar. handled=false hands the key back
// to the pager, mirroring Rust handle_search_key returning false for the paging
// keys and for every key once a match has been accepted.
func (o *TranscriptOverlay) HandleFindKey(msg bubbletea.KeyMsg) (bool, bubbletea.Cmd) {
	if o == nil || !o.find.active() {
		return false, nil
	}
	switch msg.Type {
	case bubbletea.KeyEsc, bubbletea.KeyCtrlC:
		// Rust: Esc cancels while editing, and returns to the latest output once
		// a match is accepted.
		o.CancelFind()
		if msg.Type == bubbletea.KeyEsc {
			o.viewport.GotoBottom()
		}
		return true, nil
	case bubbletea.KeyCtrlN:
		o.FindNext()
		return true, nil
	case bubbletea.KeyCtrlP:
		o.FindPrevious()
		return true, nil
	}
	if !o.find.editing {
		return false, nil
	}
	switch msg.Type {
	case bubbletea.KeyEnter:
		o.AcceptFind()
		return true, nil
	case bubbletea.KeyPgUp, bubbletea.KeyPgDown:
		// Rust returns false so paging keeps driving the viewport.
		return false, nil
	}
	o.editFindQuery(msg)
	return true, nil
}

// editFindQuery mirrors the query-editing branch of Rust handle_search_key:
// runes edit the query under the 4 KiB cap and every other key is swallowed so
// the pager's single-letter bindings (q, j, k, ...) cannot fire mid-query.
func (o *TranscriptOverlay) editFindQuery(msg bubbletea.KeyMsg) {
	switch msg.Type {
	case bubbletea.KeyBackspace:
		if o.find.query == "" {
			return
		}
		runes := []rune(o.find.query)
		o.find.query = string(runes[:len(runes)-1])
		o.find.truncated = false
		o.refreshFindMatches()
	case bubbletea.KeyRunes, bubbletea.KeySpace:
		if msg.Paste || msg.Alt {
			return
		}
		text := string(msg.Runes)
		if msg.Type == bubbletea.KeySpace {
			text = " "
			if len(msg.Runes) > 0 && string(msg.Runes) != " " {
				return
			}
		}
		if text == "" {
			return
		}
		if len(o.find.query)+len(text) > findQueryBytes {
			o.find.truncated = true
			return
		}
		o.find.query += text
		o.refreshFindMatches()
	}
}

// refreshFindMatches re-scans the transcript for the folded query, mirroring
// Rust query_changed plus the literal, case-insensitive scan in find_literal.
func (o *TranscriptOverlay) refreshFindMatches() {
	o.find.matches = nil
	o.find.index = -1
	o.find.progress = findProgressIdle
	if !o.find.active() || o.find.query == "" {
		o.refreshFindContent()
		return
	}
	folded := strings.ToLower(o.find.query)
	for index, line := range strings.Split(o.content, "\n") {
		if strings.Contains(strings.ToLower(line), folded) {
			o.find.matches = append(o.find.matches, index)
		}
	}
	if len(o.find.matches) == 0 {
		o.find.progress = findProgressExhausted
		o.refreshFindContent()
		return
	}
	o.find.index = o.firstFindMatchFromViewport()
	o.find.progress = findProgressFound
	o.scrollToFindMatch()
	o.refreshFindContent()
}

// firstFindMatchFromViewport mirrors Rust scanning forward from the current
// reading position: the first match at or below the viewport top, otherwise the
// last match above it.
func (o *TranscriptOverlay) firstFindMatchFromViewport() int {
	top := o.viewport.YOffset
	for index, line := range o.find.matches {
		if line >= top {
			return index
		}
	}
	return len(o.find.matches) - 1
}

// scrollToFindMatch places the match with Rust's third-of-a-viewport lead-in
// (search_presentation.rs context_rows).
func (o *TranscriptOverlay) scrollToFindMatch() {
	line, ok := o.find.matchLine()
	if !ok {
		return
	}
	offset := line - o.viewport.Height/3
	if offset < 0 {
		offset = 0
	}
	o.viewport.SetYOffset(offset)
}

// refreshFindContent re-renders the viewport so the matched line is reversed.
func (o *TranscriptOverlay) refreshFindContent() {
	if o == nil {
		return
	}
	o.viewport.SetContent(o.viewContent())
}
