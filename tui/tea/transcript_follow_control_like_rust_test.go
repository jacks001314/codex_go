package tea

import (
	"fmt"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Rust transcript_view::follow_control paints a centered return affordance in
// the composer gap only while the final transcript row is off screen.
func TestTranscriptFollowControlAppearsOnlyOffTheTailLikeRust(t *testing.T) {
	model := scrollableTranscript(t)
	if view := utils.StripANSI(model.View()); strings.Contains(view, "Back to bottom") {
		t.Fatalf("follow control rendered at the tail:\n%s", view)
	}

	model.activityFollow = false
	model.transcript.SetYOffset(2)
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "New activity") && !strings.Contains(view, "Back to bottom") {
		t.Fatalf("follow control missing while paused:\n%s", view)
	}
	if !strings.Contains(view, "\u2193 Back to bottom \u00b7 esc") {
		t.Fatalf("full-width follow control missing:\n%s", view)
	}
	if model.followControlRow < 0 || model.followControlWidth <= 0 {
		t.Fatalf("follow control hit area = row %d width %d", model.followControlRow, model.followControlWidth)
	}
}

// New output while the reader is paused flags unseen activity until the view
// returns to the live tail.
func TestTranscriptFollowControlTracksUnseenActivityLikeRust(t *testing.T) {
	model := scrollableTranscript(t)
	model.View()
	model.activityFollow = false
	model.transcript.SetYOffset(2)
	model.View()

	model.State.AddMessage(codextui.RoleAssistant, "fresh output while paused")
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "New activity \u00b7 \u2193 Back to bottom \u00b7 esc") {
		t.Fatalf("unseen activity control missing:\n%s", view)
	}
	if !model.transcriptUnseenActivity {
		t.Fatal("unseen activity was not flagged")
	}
}

func TestTranscriptFollowControlEscAndEnterReturnToLatestLikeRust(t *testing.T) {
	for _, key := range []bubbletea.KeyType{bubbletea.KeyEsc, bubbletea.KeyEnter} {
		model := scrollableTranscript(t)
		model.View()
		model.activityFollow = false
		model.transcript.SetYOffset(2)
		model.State.AddMessage(codextui.RoleAssistant, "new output")
		model.View()

		model.Update(bubbletea.KeyMsg{Type: key})
		if !model.activityFollow || !model.transcript.AtBottom() {
			t.Fatalf("key %v did not return to latest: follow=%v offset=%d", key, model.activityFollow, model.transcript.YOffset)
		}
		if model.transcriptUnseenActivity {
			t.Fatalf("key %v left unseen activity set", key)
		}
		if view := utils.StripANSI(model.View()); strings.Contains(view, "Back to bottom") {
			t.Fatalf("key %v kept the follow control:\n%s", key, view)
		}
	}
}

func TestTranscriptFollowControlClickReturnsToLatestLikeRust(t *testing.T) {
	model := scrollableTranscript(t)
	model.View()
	model.activityFollow = false
	model.transcript.SetYOffset(2)
	model.View()
	if model.followControlRow < 0 {
		t.Fatal("follow control was not painted")
	}
	x := model.followControlColumn + model.followControlWidth/2
	y := model.followControlRow
	model.Update(bubbletea.MouseMsg{X: x, Y: y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft})
	model.Update(bubbletea.MouseMsg{X: x, Y: y, Action: bubbletea.MouseActionRelease, Button: bubbletea.MouseButtonLeft})
	if !model.activityFollow || !model.transcript.AtBottom() {
		t.Fatalf("click did not return to latest: follow=%v offset=%d", model.activityFollow, model.transcript.YOffset)
	}
}

func TestTranscriptFollowControlShortensWithWidthLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	for i := 0; i < 40; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("event %02d\nmore detail", i))
	}
	model := NewModel(state, Options{Width: 12, Height: 12})
	model.View()
	model.activityFollow = false
	model.transcript.SetYOffset(2)
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "\u2193 Bottom") {
		t.Fatalf("narrow follow control missing:\n%s", view)
	}
	if strings.Contains(view, "Back to bottom") {
		t.Fatalf("narrow follow control did not shorten:\n%s", view)
	}
}

func TestTranscriptFollowFooterShowsActivityHistoryAndNavigationLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	for i := 0; i < 40; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("event %02d\nmore detail", i))
	}
	model := NewModel(state, Options{Width: 120, Height: 12})
	model.View()
	model.activityFollow = false
	model.transcript.SetYOffset(2)
	model.State.AddMessage(codextui.RoleAssistant, "new output")
	view := utils.StripANSI(model.View())
	want := "New activity \u00b7 Earlier messages available.  enter/esc latest \u00b7 ? shortcuts"
	if !strings.Contains(view, want) {
		t.Fatalf("footer missing %q:\n%s", want, view)
	}
}

func TestTranscriptFollowFooterUsesEscapeWhenComposerHasDraftLikeRust(t *testing.T) {
	model := scrollableTranscript(t)
	model.View()
	model.activityFollow = false
	model.transcript.SetYOffset(2)
	model.transcriptHistoryPartial = true
	model.State.AddMessage(codextui.RoleAssistant, "new output")
	model.setComposerPrompt("draft")
	view := utils.StripANSI(model.View())
	want := "New activity \u00b7 Earlier messages available.  esc latest"
	if !strings.Contains(view, want) {
		t.Fatalf("footer missing %q:\n%s", want, view)
	}
	if strings.Contains(view, "esc latest \u00b7 ? shortcuts") {
		t.Fatalf("draft footer kept the shortcuts hint:\n%s", view)
	}
}
