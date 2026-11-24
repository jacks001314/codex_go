package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"
	keymsg "codex_go/tui/keymsg"
)

// Rust #62157: Esc in the watch-only reader drops the reader and returns to the
// Subagents list with the same agent selected; a second Esc closes that list and
// leaves the parent conversation on screen.
func TestSidebarSubagentReaderEscapeReturnsToListLikeRust(t *testing.T) {
	model, closed := newSidebarSubagentPaneModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	if model.activeSide == nil || !model.activeSide.WatchOnly {
		t.Fatalf("active side = %#v, want the watch-only reader", model.activeSide)
	}

	_, cmd := model.Update(keymsg.KeyMsg{Type: keymsg.KeyEsc, Code: 0x1b})
	runTeaCmd(t, model, cmd)

	if !model.sidebarChooserVisible() || !model.sidebarChooserFocused {
		t.Fatalf("chooser = open %t focused %t, want the subagent list", model.sidebarChooserOpen, model.sidebarChooserFocused)
	}
	row, ok := model.sidebarChooserSelectedAgent()
	if !ok || row.ThreadID != "agent-1" {
		t.Fatalf("selected row = %#v ok=%t, want the reader agent still selected", row, ok)
	}
	if model.activeSide != nil {
		t.Fatalf("reader survived Esc: %#v", model.activeSide)
	}
	if tabs := model.sidebarTabsFor("parent-a"); len(tabs) != 0 {
		t.Fatalf("reader tabs after Esc = %#v, want none", tabs)
	}
	if *closed != 0 {
		t.Fatalf("close callback ran %d times; the agent thread must stay open", *closed)
	}
	if model.State.ThreadID != "parent-a" || len(model.State.Messages) != 1 {
		t.Fatalf("parent conversation changed: thread=%q messages=%#v", model.State.ThreadID, model.State.Messages)
	}
	if model.notice != "" {
		t.Fatalf("notice = %q, want the list without a closing narration", model.notice)
	}

	// Release the first Esc so the view-transition guard stops swallowing it.
	model.Update(bubbletea.KeyReleaseMsg{Code: 0x1b})
	_, cmd = model.Update(keymsg.KeyMsg{Type: keymsg.KeyEsc, Code: 0x1b})
	runTeaCmd(t, model, cmd)
	if model.sidebarChooserVisible() {
		t.Fatal("second Esc left the subagent list open")
	}
	if _, split := model.renderSideWorkspace(model.width, model.height); split {
		t.Fatal("second Esc kept a side workspace on screen")
	}
}

// Rust paint_agent_reader: the reader advertises where Escape goes above the
// transcript instead of leaving the back hint to the transcript footer.
func TestSidebarSubagentReaderPaintsBackHintLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	runTeaCmd(t, model, model.switchToSidebarAgent("agent-1"))
	side := model.activeSide
	if side == nil {
		t.Fatal("no side pane opened")
	}

	rows, _, _ := model.renderPassiveSidePane(side, true, 60, 12)
	if len(rows) != 12 {
		t.Fatalf("pane rows = %d, want 12", len(rows))
	}
	if !strings.Contains(rows[0], "back to agents") || !strings.Contains(rows[0], "Watch only") {
		t.Fatalf("reader heading = %q, want the Esc back hint", rows[0])
	}
	if !strings.Contains(strings.Join(rows[1:], "\n"), "agent transcript for agent-1") {
		t.Fatalf("heading replaced the transcript rows:\n%s", strings.Join(rows, "\n"))
	}
}

// Escape only belongs to the reader while it is open; ordinary conversations
// keep backtrack priming.
func TestSidebarSubagentEscapeIgnoredWithoutReaderLikeRust(t *testing.T) {
	model, _ := newSidebarSubagentPaneModel(t)
	if cmd, handled := model.escapeWatchOnlyReader(); handled || cmd != nil {
		t.Fatalf("escape handled without a reader: cmd=%v handled=%t", cmd, handled)
	}
	if model.sidebarChooserVisible() {
		t.Fatal("escape opened the subagent list without a reader")
	}
}
