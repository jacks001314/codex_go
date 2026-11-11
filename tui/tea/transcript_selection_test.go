package tea

import (
	mousemsg "codex_go/tui/mousemsg"
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

func transcriptSelectionModel(t *testing.T, options Options) (*Model, *string) {
	t.Helper()
	var copied string
	options.Width = 100
	options.Height = 24
	options.OnClipboardWrite = func(text string) error {
		copied = text
		return nil
	}
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	state.AddMessage(codextui.RoleAssistant, "alpha beta gamma")
	model := NewModel(state, options)
	_ = model.viewString()
	return model, &copied
}

func transcriptSelectionPoint(t *testing.T, model *Model, needle string) (int, int) {
	t.Helper()
	region := model.transcriptSelectionRegion
	if region.w <= 0 || region.h <= 0 {
		t.Fatalf("transcript region not registered: %#v", region)
	}
	for index, line := range region.text {
		if column := findDisplayColumn(line, needle); column >= 0 {
			return index, column
		}
	}
	t.Fatalf("transcript text %q not found in %#v", needle, region.text)
	return 0, 0
}

func dragTranscriptSelection(t *testing.T, model *Model, from, to string) bubbletea.Cmd {
	t.Helper()
	region := model.transcriptSelectionRegion
	startRow, startX := transcriptSelectionPoint(t, model, from)
	endRow, endX := transcriptSelectionPoint(t, model, to)
	endX += max(codextui.DisplayWidth(to)-1, 0)
	_, _ = model.Update(mousemsg.MouseMsg{X: startX, Y: region.y + startRow, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft})
	_, cmd := model.Update(mousemsg.MouseMsg{X: endX, Y: region.y + endRow, Action: mousemsg.MouseActionRelease, Button: mousemsg.MouseButtonLeft})
	return cmd
}

// The owned screen captures the mouse, so the transcript must provide an
// app-owned drag selection and copy it on release.
func TestTranscriptSelectionCopiesDraggedTextLikeRust(t *testing.T) {
	model, copied := transcriptSelectionModel(t, Options{})
	cmd := dragTranscriptSelection(t, model, "alpha", "gamma")
	if !model.transcriptSelection.active {
		t.Fatal("transcript drag did not start a selection")
	}
	if view := model.viewString(); !strings.Contains(view, "\x1b[7m") {
		t.Fatalf("transcript selection was not highlighted:\n%s", utils.StripANSI(view))
	}
	runTeaCmd(t, model, cmd)
	if got, want := strings.TrimSpace(*copied), "alpha beta gamma"; got != want {
		t.Fatalf("copied transcript text = %q, want %q", got, want)
	}
	if got := model.composer.Value(); got != "" {
		t.Fatalf("composer draft = %q, want unchanged", got)
	}

	// The composer keeps accepting input after a transcript selection.
	_, _ = model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("x")})
	if got := model.composer.Value(); got != "x" {
		t.Fatalf("composer draft after typing = %q, want x", got)
	}
}

// copy_on_select=never keeps the selection for Ctrl+C and does not copy on
// release, mirroring the footer selection contract.
func TestTranscriptSelectionKeyboardCopyLikeRust(t *testing.T) {
	model, copied := transcriptSelectionModel(t, Options{CopyOnSelect: "never"})
	cmd := dragTranscriptSelection(t, model, "alpha", "gamma")
	if cmd != nil {
		t.Fatal("copy_on_select=never copied on release")
	}
	if !model.transcriptSelection.active {
		t.Fatal("copy_on_select=never dropped the selection")
	}
	_, copyCmd := model.Update(key(keymsg.KeyCtrlC))
	runTeaCmd(t, model, copyCmd)
	if got := strings.TrimSpace(*copied); got != "alpha beta gamma" {
		t.Fatalf("Ctrl+C copied %q", got)
	}
	if model.transcriptSelection.active {
		t.Fatal("Ctrl+C did not clear the transcript selection")
	}
}

func TestTranscriptSelectionStationaryClickAndInlineModeLikeRust(t *testing.T) {
	model, copied := transcriptSelectionModel(t, Options{})
	row, _ := transcriptSelectionPoint(t, model, "alpha")
	y := model.transcriptSelectionRegion.y + row
	_, cmd := model.Update(mousemsg.MouseMsg{X: 0, Y: y, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft})
	_, cmd = model.Update(mousemsg.MouseMsg{X: 0, Y: y, Action: mousemsg.MouseActionRelease, Button: mousemsg.MouseButtonLeft})
	if cmd != nil || *copied != "" {
		t.Fatalf("stationary click copied %q", *copied)
	}
	if model.transcriptSelection.active {
		t.Fatal("stationary click left a transcript selection active")
	}

	inlineOff := false
	inline, inlineCopied := transcriptSelectionModel(t, Options{FullscreenTranscript: &inlineOff})
	if inline.transcriptSelectionRegion.w != 0 {
		t.Fatal("inline mode registered an app-owned transcript selection")
	}
	_, _ = inline.Update(mousemsg.MouseMsg{X: 0, Y: 0, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft})
	if inline.transcriptSelection.active {
		t.Fatal("inline mode started an app-owned transcript selection")
	}
	_ = inlineCopied
}

// The wheel clears the transcript selection and still scrolls the pane.
func TestTranscriptSelectionWheelClearsAndScrollsLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	for index := 0; index < 40; index++ {
		state.AddMessage(codextui.RoleSystem, "event\nmore detail")
	}
	model := NewModel(state, Options{Width: 100, Height: 12, CopyOnSelect: "never"})
	_ = model.viewString()
	cmd := dragTranscriptSelection(t, model, "event", "detail")
	if cmd != nil {
		t.Fatal("copy_on_select=never copied on release")
	}
	if !model.transcriptSelection.active {
		t.Fatal("transcript drag did not leave a selection active")
	}

	before := model.TranscriptYOffset()
	_, _ = model.Update(mousemsg.MouseMsg{Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonWheelUp})
	if model.transcriptSelection.active {
		t.Fatal("wheel left the transcript selection active")
	}
	if got, want := model.TranscriptYOffset(), before-1; got != want {
		t.Fatalf("wheel transcript offset = %d, want %d", got, want)
	}
}
