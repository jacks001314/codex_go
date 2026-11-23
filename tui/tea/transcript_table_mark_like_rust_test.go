package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	keymsg "codex_go/tui/keymsg"
)

func tableSelectionModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-table")
	state.AddMessage(codextui.RoleAssistant, "| Task | Status |\n|---|---|\n| One | Done |\n| Two | Open |")
	model := NewModel(state, Options{Width: 80, Height: 16})
	model.now = fixedTeaTime
	return model
}

// Rust #63047: `pager.mark_markdown` defaults to bare `m` and marks the table
// cell under the caret before any enclosing Markdown block.
func TestMarkMarkdownKeyMarksTableCellLikeRust(t *testing.T) {
	model := tableSelectionModel(t)
	model.Update(key(keymsg.KeyCtrlX))
	model.Update(runes("["))
	if model.overlay == nil || !model.overlay.SelectionActive() {
		t.Fatalf("selection mode did not start (overlay=%v)", model.overlay != nil)
	}
	model.Update(runes("m"))
	if !model.overlay.SelectionMarked() {
		t.Fatal("pager.mark_markdown did not mark a table cell")
	}
	if got := strings.TrimSpace(model.overlay.SelectedText()); got == "" {
		t.Fatal("marked table cell has no text")
	}
}

// The chord only exists while selection mode owns the transcript, matching
// Rust keymap/chords.rs contains_action (TRANSCRIPT_SELECT).
func TestMarkMarkdownLeaderIgnoredOutsideSelectionLikeRust(t *testing.T) {
	model := tableSelectionModel(t)
	model.Update(key(keymsg.KeyCtrlT))
	if model.overlay == nil || model.overlay.SelectionActive() {
		t.Fatalf("transcript overlay not open in browse mode (overlay=%v)", model.overlay != nil)
	}
	model.Update(key(keymsg.KeyCtrlX))
	pending := model.leaderChordPending
	model.Update(runes("v"))
	if model.overlay.SelectionActive() || model.overlay.SelectionMarked() {
		t.Fatalf("leader v outside selection started a mark (pending=%v)", pending)
	}
}
