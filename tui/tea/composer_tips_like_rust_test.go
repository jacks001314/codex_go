package tea

import (
	mousemsg "codex_go/tui/mousemsg"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func startupTipTestModel(t *testing.T, width int, fullscreen bool) *Model {
	t.Helper()
	show := true
	// A resumed session keeps the tooltip path: only a fresh session's first
	// event renders the startup help block (Rust #58127).
	state := codextui.NewState(nil)
	state.ThreadID = "thread-startup-tip"
	model := NewModel(state, Options{
		Width:                width,
		Height:               30,
		FullscreenTranscript: &fullscreen,
		ShowTooltips:         &show,
	})
	model.addStartupSessionHeader("1.0.0")
	model.viewString()
	model.activityFollow = true
	return model
}

// Rust #47954: the startup tip lives in the transcript, so the fullscreen
// composer gap stays blank and the passive footer keeps `? for shortcuts`.
func TestStartupTipStaysOutOfFullscreenComposerGapLikeRust(t *testing.T) {
	model := startupTipTestModel(t, 100, true)
	view := model.viewString()
	if strings.Contains(view, "**Tip:**") {
		t.Fatalf("fullscreen composer gap rendered a startup tip:\n%s", view)
	}
	// #46739 adds a separate contextual discovery tip. The startup announcement
	// still belongs to the transcript.
	if strings.Contains(view, "Tip: ") && !strings.Contains(view, "Tip: @ mentions files, skills, and plugins") {
		t.Fatalf("fullscreen composer gap rendered the wrong tip:\n%s", view)
	}
	footer, _ := model.footerHelpLine()
	if footer != footerHelpText {
		t.Fatalf("footer = %q, want %q", footer, footerHelpText)
	}
}

// The retained startup tip row keeps rendering in the inline transcript.
func TestStartupTipRendersInInlineTranscriptLikeRust(t *testing.T) {
	model := startupTipTestModel(t, 100, false)
	found := false
	for _, message := range model.State.Messages {
		if !message.StartupTip {
			continue
		}
		found = true
		if !strings.Contains(message.Text, "Tip:") {
			t.Fatalf("startup tip row = %q", message.Text)
		}
	}
	if !found {
		t.Fatal("inline transcript dropped the startup tip row")
	}
}

func TestComposerGapCtrlClickOpensOSC8LinkLikeRust(t *testing.T) {
	model := startupTipTestModel(t, 100, true)
	line := "Tip: " + codextui.OSC8Hyperlink("https://example.com/docs", "docs") + " end"
	model.renderedSelection.regions[renderedRegionFooter] = renderedRegion{
		kind: renderedRegionFooter,
		y:    10,
		x:    0,
		h:    1,
		text: []string{line},
	}
	opened := ""
	model.onOpenURL = func(url string) error {
		opened = url
		return nil
	}
	handled, cmd := model.handleComposerGapLinkClick(mousemsg.MouseMsg{
		X: 7, Y: 10, Ctrl: true, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
	})
	if !handled || cmd == nil {
		t.Fatalf("Ctrl-click handled=%v cmd=%v", handled, cmd != nil)
	}
	model.Update(cmd())
	if opened != "https://example.com/docs" {
		t.Fatalf("opened = %q", opened)
	}

	if handled, _ := model.handleComposerGapLinkClick(mousemsg.MouseMsg{
		X: 7, Y: 10, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
	}); handled {
		t.Fatal("plain click consumed a link hit test")
	}
}

// A fetched announcement replaces the transcript tip while the session is still
// at its first turn; Rust prewarms the same cache before the first frame.
func TestStartupAnnouncementReplacesTranscriptTipLikeRust(t *testing.T) {
	model := startupTipTestModel(t, 100, false)
	model.onFetchStartupAnnouncement = func() (string, bool) {
		return "Fetched **announcement**", true
	}
	cmd := model.startupAnnouncementCmd()
	if cmd == nil {
		t.Fatal("startup announcement command missing")
	}
	model.Update(cmd())
	tip := utils.StripANSI(startupTipMessageText(model))
	if !strings.Contains(tip, "Fetched announcement") {
		t.Fatalf("transcript tip = %q", tip)
	}

	model.State.AddMessage(codextui.RoleUser, "first turn")
	model.Update(StartupAnnouncementMsg{Text: "Late **announcement**", OK: true})
	tip = utils.StripANSI(startupTipMessageText(model))
	if strings.Contains(tip, "Late announcement") {
		t.Fatalf("announcement replaced the tip after the first turn: %q", tip)
	}
}

func startupTipMessageText(model *Model) string {
	for _, message := range model.State.Messages {
		if message.StartupTip {
			return message.Text
		}
	}
	return ""
}
