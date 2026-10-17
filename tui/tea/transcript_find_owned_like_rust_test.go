package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func ownedFindFixture(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.AddHistoryLines(
		[]string{"Ran older", "  +2 lines (ctrl+t to view transcript)"},
		[]string{"$ older command", "older hidden needle", "older hidden detail"},
	)
	state.AddHistoryLines(
		[]string{"Ran newer", "  +2 lines (ctrl+t to view transcript)"},
		[]string{"$ newer command", "newer hidden needle", "newer hidden detail"},
	)
	return NewModel(state, Options{Width: 80, Height: 16})
}

// TestOwnedTranscriptFindExpandsOnlyCurrentMatchLikeRust covers Rust #50045:
// Find searches hidden command output without expanding the whole transcript,
// and moving between matches transfers the temporary expansion.
func TestOwnedTranscriptFindExpandsOnlyCurrentMatchLikeRust(t *testing.T) {
	model := ownedFindFixture(t)
	model.Update(agentsF3Event())
	for _, r := range "needle" {
		model.Update(findKeyRunes(string(r)))
	}
	if got := len(model.mainFind.matches); got != 2 {
		t.Fatalf("match count = %d, want both hidden matches", got)
	}

	// The newest match is selected first. Only that command's hidden output is
	// revealed; the older command remains compact.
	view := model.View()
	if !strings.Contains(view, "newer hidden needle") {
		t.Fatalf("newest hidden match was not revealed:\n%s", view)
	}
	if strings.Contains(view, "older hidden needle") {
		t.Fatalf("an unselected hidden match was expanded:\n%s", view)
	}
	if !strings.Contains(view, "Ran older") || !strings.Contains(view, "ctrl+t to view transcript") {
		t.Fatalf("older command did not retain its compact presentation:\n%s", view)
	}

	// Ctrl+P moves to the older hit and collapses the newer one.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlP})
	view = model.View()
	if !strings.Contains(view, "older hidden needle") {
		t.Fatalf("older hidden match was not revealed:\n%s", view)
	}
	if strings.Contains(view, "newer hidden needle") {
		t.Fatalf("previous match stayed expanded:\n%s", view)
	}
	if !strings.Contains(view, "Ran newer") {
		t.Fatalf("newer command disappeared instead of collapsing:\n%s", view)
	}

	// Ctrl+N returns to the newer hit; running past the end keeps it visible.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlN})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlN})
	view = model.View()
	if !strings.Contains(view, "newer hidden needle") {
		t.Fatalf("newer match was not retained after exhaustion:\n%s", view)
	}
	if !strings.Contains(view, "No more matches") {
		t.Fatalf("exhaustion status missing:\n%s", view)
	}

	// Enter accepts the result; Esc then returns to the latest output and clears
	// the temporary expansion.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if !model.mainFind.accepted || model.mainFind.editing {
		t.Fatalf("accept left editing=%v accepted=%v", model.mainFind.editing, model.mainFind.accepted)
	}
	model.Update(agentsF3Event())
	if !model.mainFind.editing || model.mainFind.query != "needle" {
		t.Fatalf("reopening Find lost the query: editing=%v query=%q", model.mainFind.editing, model.mainFind.query)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.mainFind.active() {
		t.Fatal("Esc did not clear the accepted Find")
	}
	view = model.View()
	if strings.Contains(view, "hidden needle") {
		t.Fatalf("cancelled Find left hidden output expanded:\n%s", view)
	}
}

// TestOwnedTranscriptFindRevealsTranscriptOnlyMatchLikeRust covers the other
// hidden source: a transcript-only reasoning block that the compact transcript
// normally omits.
func TestOwnedTranscriptFindRevealsTranscriptOnlyMatchLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.Messages = append(state.Messages, codextui.Message{
		Role:           codextui.RoleHistory,
		Text:           "hidden reasoning needle",
		TranscriptOnly: true,
	})
	state.BumpMessagesRevision()
	model := NewModel(state, Options{Width: 80, Height: 12})
	model.Update(agentsF3Event())
	for _, r := range "needle" {
		model.Update(findKeyRunes(string(r)))
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "hidden reasoning needle") {
		t.Fatalf("transcript-only match was not revealed:\n%s", view)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	view = utils.StripANSI(model.View())
	if strings.Contains(view, "hidden reasoning needle") {
		t.Fatalf("transcript-only match stayed visible after cancel:\n%s", view)
	}
}

// TestOwnedTranscriptFindPicksUpLiveMessageUpdatesLikeRust covers the live
// tail path used by an active exec cell: rerenderActiveExecCell upserts the
// current cell into State.Messages, and an open Find must rescan that revision.
func TestOwnedTranscriptFindPicksUpLiveMessageUpdatesLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.AddMessage(codextui.RoleSystem, "turn started")
	model := NewModel(state, Options{Width: 80, Height: 16})
	model.Update(agentsF3Event())
	for _, r := range "needle" {
		model.Update(findKeyRunes(string(r)))
	}
	if len(model.mainFind.matches) != 0 {
		t.Fatalf("unexpected pre-live matches: %#v", model.mainFind.matches)
	}

	model.upsertHistoryMessageWithTranscript(
		-1,
		[]string{"Ran live command", "  +2 lines (ctrl+t to view transcript)"},
		[]string{"$ live command", "live hidden needle", "live tail"},
		[]string{"$ live command", "live hidden needle", "live tail"},
	)
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "live hidden needle") {
		t.Fatalf("open Find did not rescan the live message update:\n%s", view)
	}
	if got := len(model.mainFind.matches); got != 1 {
		t.Fatalf("live match count = %d, want 1", got)
	}
}

// TestOwnedTranscriptFindRetainsSelectionAcrossResizeLikeRust keeps the
// selected message and query while re-wrapping the transcript at a new width.
func TestOwnedTranscriptFindRetainsSelectionAcrossResizeLikeRust(t *testing.T) {
	model := ownedFindFixture(t)
	model.Update(agentsF3Event())
	for _, r := range "needle" {
		model.Update(findKeyRunes(string(r)))
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlP})
	selected, ok := model.mainFind.selected()
	if !ok || selected.message != 0 {
		t.Fatalf("selected match before resize = %#v ok=%v", selected, ok)
	}
	model.Update(bubbletea.WindowSizeMsg{Width: 50, Height: 12})
	selected, ok = model.mainFind.selected()
	if !ok || selected.message != 0 || model.mainFind.query != "needle" {
		t.Fatalf("selection after resize = %#v ok=%v query=%q", selected, ok, model.mainFind.query)
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "older hidden needle") {
		t.Fatalf("older match was not retained after resize:\n%s", view)
	}
	if strings.Contains(view, "newer hidden needle") {
		t.Fatalf("resize expanded the unselected newer match:\n%s", view)
	}
}
