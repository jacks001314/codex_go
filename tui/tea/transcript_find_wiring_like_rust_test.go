package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

func findKeyRunes(text string) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune(text)}
}

// newFindTranscriptModel opens the transcript pager over a fixture with two
// matches for `needle`.
func newFindTranscriptModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.AddMessage(codextui.RoleSystem, "alpha needle one\nbeta detail")
	state.AddMessage(codextui.RoleSystem, "gamma detail")
	state.AddMessage(codextui.RoleSystem, "delta needle two\nepsilon detail")
	model := NewModel(state, Options{Width: 60, Height: 10})
	model.openTranscriptOverlay()
	if model.overlay == nil || !model.overlayTranscript {
		t.Fatalf("transcript overlay did not open")
	}
	return model
}

// TestTranscriptFindKeyWiringLikeRust pins the app-side wiring of Rust
// app_backtrack/legacy_input.rs:34: `pager.find` (`/` or f3, keymap.rs:1929)
// opens the transcript pager's find bar, the bar then owns the keyboard, and Esc
// hands it back. Removing the wiring leaves the catalog entry with no consumer,
// which this test rejects.
func TestTranscriptFindKeyWiringLikeRust(t *testing.T) {
	model := newFindTranscriptModel(t)

	// `/` is Rust pager.find: it must open the find bar instead of scrolling.
	model.Update(findKeyRunes("/"))
	if !model.overlay.FindEditing() {
		t.Fatalf("`/` did not open the transcript find bar")
	}

	for _, r := range "needle" {
		model.Update(findKeyRunes(string(r)))
	}
	if got := model.overlay.FindQuery(); got != "needle" {
		t.Fatalf("query = %q, want needle", got)
	}
	if got := model.overlay.FindMatchCount(); got != 2 {
		t.Fatalf("match count = %d, want 2", got)
	}

	// The bar opens at the bottom, so the found match is a real match line; the
	// ctrl+p/ctrl+n pair then walks between the two matches (Rust
	// next_search_match: older is ctrl+p, newer is ctrl+n, no wraparound).
	newest, ok := model.overlay.FindMatchLine()
	if !ok {
		t.Fatalf("no match line selected after typing the query")
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlP})
	older, ok := model.overlay.FindMatchLine()
	if !ok || older >= newest {
		t.Fatalf("ctrl+p match line = %d ok=%v, want a line before %d", older, ok, newest)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlN})
	back, ok := model.overlay.FindMatchLine()
	if !ok || back != newest {
		t.Fatalf("ctrl+n match line = %d ok=%v, want %d", back, ok, newest)
	}
	if got := model.overlay.FindMatchCount(); got != 2 {
		t.Fatalf("navigation changed the match count to %d, want 2", got)
	}

	// The bar owns the keyboard: `q` must edit the query, not close the overlay.
	model.Update(findKeyRunes("q"))
	if model.overlay == nil {
		t.Fatalf("`q` closed the overlay while the find bar owned the keyboard")
	}
	if got := model.overlay.FindQuery(); got != "needleq" {
		t.Fatalf("query after `q` = %q, want needleq", got)
	}

	// Esc closes the bar and restores the pager keyboard.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.overlay.FindActive() || model.overlay.FindQuery() != "" {
		t.Fatalf("Esc left active=%v query=%q", model.overlay.FindActive(), model.overlay.FindQuery())
	}

	// With the bar closed the pager keymap works again: `q` closes the overlay.
	model.Update(findKeyRunes("q"))
	if model.overlay != nil {
		t.Fatalf("`q` did not close the transcript overlay after the find bar closed")
	}
}

// TestTranscriptPagerKeysUnchangedWhileFindingStopsLikeRust keeps the existing
// pager bindings honest: scrolling still works when the bar is closed.
func TestTranscriptPagerScrollStillWorksLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	for i := 0; i < 40; i++ {
		state.AddMessage(codextui.RoleSystem, "event detail")
	}
	model := NewModel(state, Options{Width: 60, Height: 10})
	model.openTranscriptOverlay()
	before := model.overlay.YOffset()
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyPgUp})
	if got := model.overlay.YOffset(); got >= before {
		t.Fatalf("PageUp offset = %d, want less than %d", got, before)
	}
	if model.overlay.FindActive() {
		t.Fatalf("PageUp opened the find bar")
	}
	if _, ok := codextui.FindKeymapAction("pager", chatwidget.PagerFind); !ok {
		t.Fatalf("pager.find missing from the catalog")
	}
}
