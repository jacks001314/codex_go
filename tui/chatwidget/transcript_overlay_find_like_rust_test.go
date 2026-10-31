package chatwidget

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// Rust parity: keymap_setup/actions.rs:256 declares `pager.find`; keymap.rs:1929
// binds it to `f3` and `/`; app_backtrack/legacy_input.rs:34 calls
// TranscriptView::begin_search. This pins the catalog half plus the find bar's
// real behavior (T-b13 §T13).
func TestTranscriptPagerFindCatalogLikeRust(t *testing.T) {
	descriptor, ok := codextui.FindKeymapAction("pager", PagerFind)
	if !ok {
		t.Fatalf("pager.find is missing from keymapActionCatalog")
	}
	if descriptor.Context != "pager" || descriptor.Label == "" {
		t.Fatalf("pager.find descriptor = %#v", descriptor)
	}
	bindings, source, custom := codextui.ResolvedKeymapBindings(nil, "pager", PagerFind)
	if custom || source != "default" || strings.Join(bindings, ",") != "f3,/" {
		t.Fatalf("pager.find default = %#v source=%q custom=%v, want f3,/", bindings, source, custom)
	}
	config := codextui.NewKeymapConfig()
	if err := config.Set("pager", PagerFind, []string{"f4"}); err != nil {
		t.Fatalf("Set pager.find = %v", err)
	}
	if bindings, source, custom := codextui.ResolvedKeymapBindings(config, "pager", PagerFind); !custom || source != "custom" || strings.Join(bindings, ",") != "f4" {
		t.Fatalf("custom pager.find = %#v source=%q custom=%v, want f4", bindings, source, custom)
	}
}

func findKeyRunes(text string) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune(text)}
}

func typeFindQuery(t *testing.T, overlay *TranscriptOverlay, text string) {
	t.Helper()
	for _, r := range text {
		if handled, _ := overlay.HandleFindKey(findKeyRunes(string(r))); !handled {
			t.Fatalf("typing %q was not handled by the find bar", string(r))
		}
	}
}

func linesMatching(content string, query string) []int {
	folded := strings.ToLower(query)
	var matches []int
	for index, line := range strings.Split(content, "\n") {
		if strings.Contains(strings.ToLower(line), folded) {
			matches = append(matches, index)
		}
	}
	return matches
}

// TestTranscriptOverlayFindBarLikeRust covers the find bar's real behavior:
// opening it takes the keyboard, typing edits the query, matches/non-matches are
// reported with Rust's own status wording, ctrl+n/ctrl+p walk matches without
// wrapping, and Esc closes the bar.
func TestTranscriptOverlayFindBarLikeRust(t *testing.T) {
	content := "alpha needle one\nbeta\ngamma needle two\ndelta"
	overlay := NewTranscriptOverlay(60, 10, content)
	if overlay.FindActive() {
		t.Fatalf("find bar is active before pager.find")
	}

	overlay.BeginFind()
	if !overlay.FindEditing() || !overlay.FindActive() {
		t.Fatalf("BeginFind left editing=%v active=%v", overlay.FindEditing(), overlay.FindActive())
	}
	if got := overlay.FindStatusLine(); got != "Type to find" {
		t.Fatalf("empty-query status = %q, want %q", got, "Type to find")
	}
	if bar := overlay.FindBarLine(); !strings.HasPrefix(bar, "Find: ") || !strings.Contains(bar, "esc cancel") {
		t.Fatalf("empty-query find bar = %q", bar)
	}

	typeFindQuery(t, overlay, "needle")
	if got := overlay.FindQuery(); got != "needle" {
		t.Fatalf("query = %q, want needle", got)
	}
	want := linesMatching(content, "needle")
	if got := overlay.FindMatchCount(); got != len(want) {
		t.Fatalf("match count = %d, want %d", got, len(want))
	}
	if len(want) == 0 {
		t.Fatalf("fixture has no matches")
	}
	line, ok := overlay.FindMatchLine()
	if !ok || line != want[0] {
		t.Fatalf("first match line = %d ok=%v, want %d", line, ok, want[0])
	}
	if !strings.Contains(overlay.FindBarLine(), "enter accept") {
		t.Fatalf("found bar = %q", overlay.FindBarLine())
	}

	// Matches never wrap: ctrl+n walks newer, and running past the end reports
	// Rust's "No more matches" while the current match stays on screen.
	if !overlay.FindNext() {
		t.Fatalf("ctrl+n did not advance to the second match")
	}
	if line, _ := overlay.FindMatchLine(); line != want[len(want)-1] {
		t.Fatalf("second match line = %d, want %d", line, want[len(want)-1])
	}
	if overlay.FindNext() {
		t.Fatalf("ctrl+n wrapped past the last match")
	}
	if got := overlay.FindStatusLine(); !strings.HasPrefix(got, "No more matches") {
		t.Fatalf("exhausted status = %q", got)
	}
	if !overlay.FindPrevious() {
		t.Fatalf("ctrl+p did not walk back to the first match")
	}

	// A query with no hits reports Rust's "No matches".
	typeFindQuery(t, overlay, "zzz")
	if got := overlay.FindMatchCount(); got != 0 {
		t.Fatalf("miss count = %d, want 0", got)
	}
	if got := overlay.FindStatusLine(); got != "No matches" {
		t.Fatalf("miss status = %q, want %q", got, "No matches")
	}

	// Esc closes the bar (Rust Search::cancel_search clears the query too).
	if handled, _ := overlay.HandleFindKey(bubbletea.KeyMsg{Type: bubbletea.KeyEsc}); !handled {
		t.Fatalf("Esc was not handled by the find bar")
	}
	if overlay.FindActive() || overlay.FindQuery() != "" {
		t.Fatalf("Esc left active=%v query=%q", overlay.FindActive(), overlay.FindQuery())
	}
	if bar := overlay.FindBarLine(); bar != "" {
		t.Fatalf("closed find bar line = %q", bar)
	}
}

// TestTranscriptOverlayFindBarOwnsKeyboardLikeRust pins the routing rule from
// Rust handle_search_key: while the query editor is open every plain key edits
// the query (so `q` cannot close the pager and `j`/`k` cannot scroll), and the
// paging keys still reach the viewport.
func TestTranscriptOverlayFindBarOwnsKeyboardLikeRust(t *testing.T) {
	overlay := NewTranscriptOverlay(60, 6, numberedTranscript(40))
	overlay.BeginFind()
	typeFindQuery(t, overlay, "line 1")
	if got := overlay.FindMatchCount(); got != 10 {
		t.Fatalf("match count for `line 1` = %d, want 10", got)
	}
	scrolled := overlay.YOffset()
	if scrolled <= 0 {
		t.Fatalf("find did not scroll the viewport onto the match: offset = %d", scrolled)
	}
	for _, key := range []bubbletea.KeyMsg{findKeyRunes("q"), findKeyRunes("j"), findKeyRunes("k")} {
		if handled, _ := overlay.HandleFindKey(key); !handled {
			t.Fatalf("key %q leaked past the find bar", key.String())
		}
	}
	if got := overlay.FindQuery(); got != "line 1qjk" {
		t.Fatalf("query = %q, want line 1qjk", got)
	}
	if got := overlay.FindStatusLine(); got != "No matches" {
		t.Fatalf("status after the keys = %q, want %q", got, "No matches")
	}
	// Rust returns false for paging keys so the pager still scrolls.
	handled, _ := overlay.HandleFindKey(bubbletea.KeyMsg{Type: bubbletea.KeyPgDown})
	if handled {
		t.Fatalf("PageDown should fall through to the pager")
	}
	if !overlay.ApplyPagerAction(PagerPageDown) {
		t.Fatalf("PageDown pager action was refused")
	}
	if got := overlay.YOffset(); got == scrolled {
		t.Fatalf("PageDown did not scroll the pager, offset stayed %d", got)
	}
}
