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

// TestGlobalFindTranscriptStartsOwnedFindLikeRust covers the owned-screen half
// of Rust's find behavior (#50045): f3 starts Find in the compact transcript
// instead of switching to the detailed pager overlay. The overlay keeps its own
// find bar when it is already open (covered by TestTranscriptFindKeyWiringLikeRust).
func TestGlobalFindTranscriptStartsOwnedFindLikeRust(t *testing.T) {
	model := newTranscriptFindFixture(t)
	if model.overlay != nil {
		t.Fatalf("fixture already has an overlay open")
	}

	model.Update(agentsF3Event())
	if model.overlay != nil {
		t.Fatal("f3 switched to the detailed transcript pager; owned Find should stay in place")
	}
	if !model.mainFind.editing {
		t.Fatal("f3 did not start the owned transcript find editor")
	}

	for _, r := range "needle" {
		model.Update(findKeyRunes(string(r)))
	}
	if got := model.mainFind.query; got != "needle" {
		t.Fatalf("query = %q, want needle", got)
	}
	if got := len(model.mainFind.matches); got != 1 {
		t.Fatalf("match count = %d, want 1", got)
	}

	// Enter accepts the readable result; Esc then returns to latest output.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if !model.mainFind.active() || !model.mainFind.accepted || model.mainFind.query != "needle" {
		t.Fatalf("Enter did not accept the readable result: %#v", model.mainFind)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.mainFind.active() {
		t.Fatalf("Esc left accepted Find active")
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
