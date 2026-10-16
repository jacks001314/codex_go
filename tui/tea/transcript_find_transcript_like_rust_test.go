package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func newTranscriptFindFixture(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.AddMessage(codextui.RoleSystem, "alpha needle one\nbeta detail")
	state.AddMessage(codextui.RoleSystem, "gamma detail")
	return NewModel(state, Options{Width: 60, Height: 10})
}

// TestGlobalFindTranscriptOpensPagerFindLikeRust covers Rust #46734: the global
// find-transcript shortcut (f3, keymap.rs:1678) opens the transcript overlay and
// starts its search bar in the same keypress (app/input.rs:584-597). Removing the
// dispatch leaves `global.find_transcript` as a config-only entry, which this
// test rejects.
func TestGlobalFindTranscriptOpensPagerFindLikeRust(t *testing.T) {
	model := newTranscriptFindFixture(t)
	if model.overlay != nil {
		t.Fatalf("fixture already has an overlay open")
	}

	model.Update(agentsF3Event())
	if model.overlay == nil || !model.overlayTranscript {
		t.Fatal("f3 did not open the transcript overlay")
	}
	if !model.overlay.FindEditing() {
		t.Fatal("f3 opened the transcript without starting its find bar (Rust begin_search)")
	}

	for _, r := range "needle" {
		model.Update(findKeyRunes(string(r)))
	}
	if got := model.overlay.FindQuery(); got != "needle" {
		t.Fatalf("query = %q, want needle", got)
	}
	if got := model.overlay.FindMatchCount(); got != 1 {
		t.Fatalf("match count = %d, want 1", got)
	}

	// Esc leaves the pager open with the bar closed, so the search shortcut is a
	// find bar and not a close key.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.overlay == nil {
		t.Fatal("Esc closed the whole transcript overlay")
	}
	if model.overlay.FindActive() || model.overlay.FindEditing() {
		t.Fatalf("Esc left the find bar active (active=%v editing=%v)", model.overlay.FindActive(), model.overlay.FindEditing())
	}
}

// TestGlobalOpenTranscriptDoesNotStartFindLikeRust is the paired negative: ctrl-t
// still opens the pager without a find bar, so the two global shortcuts keep
// their distinct Rust behavior.
func TestGlobalOpenTranscriptDoesNotStartFindLikeRust(t *testing.T) {
	model := newTranscriptFindFixture(t)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlT})
	if model.overlay == nil || !model.overlayTranscript {
		t.Fatal("ctrl-t did not open the transcript overlay")
	}
	if model.overlay.FindActive() || model.overlay.FindEditing() {
		t.Fatalf("ctrl-t started the find bar (active=%v editing=%v)", model.overlay.FindActive(), model.overlay.FindEditing())
	}
}
