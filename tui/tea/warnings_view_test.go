package tea

import (
	keymsg "codex_go/tui/keymsg"
	mousemsg "codex_go/tui/mousemsg"
	"strings"
	"testing"

	codextui "codex_go/tui"
	historycell "codex_go/tui/history_cell"
	"codex_go/utils"
)

// TestWarningsBodySelectionCopiesLikeRust mirrors #57271: the warning body is
// selectable without changing the retained composer draft, and page navigation
// still works after a selection.
func TestWarningsBodySelectionCopiesLikeRust(t *testing.T) {
	enabled := true
	var copied string
	model := NewModel(codextui.NewState(nil), Options{
		Width:                 100,
		Height:                32,
		ShowTooltips:          &enabled,
		CopyOnSelect:          "always",
		StartupConfigWarnings: []string{"First warning detail", "Second warning detail"},
		OnClipboardWrite: func(text string) error {
			copied = text
			return nil
		},
	})
	model.composer.InsertString("draft stays")
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyF2})
	_ = model.viewString()

	region := model.renderedSelection.regions[renderedRegionWarnings]
	if region.w <= 0 || len(region.text) == 0 {
		t.Fatalf("warning body region not registered: %#v", region)
	}
	line := region.text[0]
	column := findDisplayColumn(line, "First")
	if column < 0 {
		t.Fatalf("warning body missing its first line: %q", line)
	}
	start := region.x + column
	end := start + len("First warning") - 1
	_, _ = model.Update(mousemsg.MouseMsg{X: start, Y: region.y, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft})
	_, cmd := model.Update(mousemsg.MouseMsg{X: end, Y: region.y, Action: mousemsg.MouseActionRelease, Button: mousemsg.MouseButtonLeft})
	runTeaCmd(t, model, cmd)
	if copied != "First warning" {
		t.Fatalf("copied warning selection = %q", copied)
	}
	if got := model.composer.Value(); got != "draft stays" {
		t.Fatalf("composer draft = %q, want unchanged", got)
	}
	if view := model.viewString(); !strings.Contains(view, "\x1b[7m") {
		t.Fatal("warning selection was not highlighted")
	}

	// Paging still reaches the second warning; its changed body invalidates the
	// selection but keeps the viewer in control.
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRight})
	if got := model.warningsView.CurrentIndex(); got != 1 {
		t.Fatalf("current warning after page right = %d, want 2", got+1)
	}
	if view := utils.StripANSI(model.viewString()); !strings.Contains(view, "Second warning detail") {
		t.Fatalf("paging after selection did not show the second warning:\n%s", view)
	}
}

// TestWarningsBodyWheelScrollsLikeRust covers the unmodified three-row wheel
// and its ownership of gestures inside the painted body only.
func TestWarningsBodyWheelScrollsLikeRust(t *testing.T) {
	enabled := true
	details := strings.Repeat("A long warning line that wraps across several rows. ", 30)
	model := NewModel(codextui.NewState(nil), Options{
		Width:                 90,
		Height:                32,
		ShowTooltips:          &enabled,
		StartupConfigWarnings: []string{details},
	})
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyF2})
	_ = model.viewString()
	region := model.renderedSelection.regions[renderedRegionWarnings]
	if region.w <= 0 || model.warningsView.MaxOffset() == 0 {
		t.Fatalf("warning body is not scrollable: region=%#v max=%d", region, model.warningsView.MaxOffset())
	}
	x, y := region.x+1, region.y
	model.Update(mousemsg.MouseMsg{X: x, Y: y, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonWheelDown})
	if got := model.warningsView.Offset(); got != 3 {
		t.Fatalf("unmodified wheel offset = %d, want 3", got)
	}
	model.Update(mousemsg.MouseMsg{X: x, Y: y, Ctrl: true, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonWheelDown})
	if got := model.warningsView.Offset(); got != 3 {
		t.Fatalf("modified wheel changed warning offset to %d", got)
	}
	model.Update(mousemsg.MouseMsg{X: x, Y: region.y - 1, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonWheelDown})
	if got := model.warningsView.Offset(); got != 3 {
		t.Fatalf("wheel outside body changed warning offset to %d", got)
	}
	model.Update(mousemsg.MouseMsg{X: x, Y: y, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonWheelUp})
	if got := model.warningsView.Offset(); got != 0 {
		t.Fatalf("wheel up offset = %d, want 0", got)
	}
}

// Mirrors the #48205/#48206 wiring: F2 opens the retained-warnings viewer, the
// list actions page through the diagnostics, the draft is untouched, and closing
// dismisses the pages that were drawn.
func TestWarningsViewerOpensNavigatesAndDismissesLikeRust(t *testing.T) {
	enabled := true
	model := NewModel(codextui.NewState(nil), Options{
		Width:                 100,
		Height:                30,
		ShowTooltips:          &enabled,
		StartupConfigWarnings: []string{"First warning", "Second warning"},
	})
	model.composer.InsertString("draft")

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyF2})
	if model.warningsView == nil {
		t.Fatal("F2 did not open the warnings viewer")
	}
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "Warnings \u00b7 1 of 2") || !strings.Contains(view, "First warning") {
		t.Fatalf("viewer did not show the first warning:\n%s", view)
	}
	if !strings.Contains(view, "keep & next") || !strings.Contains(view, "dismiss & close") {
		t.Fatalf("viewer footer missing hints:\n%s", view)
	}

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRight})
	view = utils.StripANSI(model.viewString())
	if !strings.Contains(view, "Warnings \u00b7 2 of 2") || !strings.Contains(view, "Second warning") {
		t.Fatalf("viewer did not page to the second warning:\n%s", view)
	}

	// Esc closes and dismisses the drawn pages; the draft is untouched.
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyEsc})
	if model.warningsView != nil {
		t.Fatal("Esc did not close the warnings viewer")
	}
	if got := strings.TrimSpace(model.composer.Value()); got != "draft" {
		t.Fatalf("composer draft = %q, want it untouched", got)
	}
	if got := model.warningDisplay.VisibleEntries([]historycell.WarningCell{model.startupWarnings}); len(got) != 0 {
		t.Fatalf("drawn warnings still visible after close: %#v", got)
	}
}

// Mirrors the keep behaviour: plain `k` keeps the current page, and the global
// copy key copies only the current diagnostic.
func TestWarningsViewerKeepAndCopyLikeRust(t *testing.T) {
	enabled := true
	model := NewModel(codextui.NewState(nil), Options{
		Width:                 100,
		Height:                30,
		ShowTooltips:          &enabled,
		StartupConfigWarnings: []string{"First warning", "Second warning"},
	})
	var copied []string
	model.clipboardWrite = func(text string) error {
		copied = append(copied, text)
		return nil
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyF2})
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'k'}})
	if got := model.warningsView.CurrentIndex(); got != 1 {
		t.Fatalf("keep did not advance the viewer: index %d", got)
	}
	updateAndRunClipboard(t, model, keymsg.KeyMsg{Type: keymsg.KeyCtrlO})
	if len(copied) != 1 || copied[0] != "Second warning" {
		t.Fatalf("copy = %#v, want the current diagnostic", copied)
	}
	if model.warningsFlash != "Copied" {
		t.Fatalf("copy flash = %q, want Copied", model.warningsFlash)
	}
	// The kept first page survives the close; the unvisited page is neither
	// dismissed nor kept.
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyEsc})
	visible := model.warningDisplay.VisibleEntries([]historycell.WarningCell{model.startupWarnings})
	if len(visible) != 2 {
		t.Fatalf("visible warnings after keep = %#v, want both", visible)
	}
}

// Mirrors Rust's `/warnings` slash command: it opens the same viewer without
// submitting anything.
func TestWarningsSlashCommandOpensViewerLikeRust(t *testing.T) {
	enabled := true
	model := NewModel(codextui.NewState(nil), Options{
		Width:                 100,
		Height:                30,
		ShowTooltips:          &enabled,
		StartupConfigWarnings: []string{"Config warning"},
	})
	invocation, ok := codextui.ParseCommand("/warnings")
	if !ok || invocation.Command != codextui.CommandWarnings {
		t.Fatalf("ParseCommand(/warnings) = %#v ok=%v", invocation, ok)
	}
	runTeaCmd(t, model, model.applyCommand(invocation))
	if model.warningsView == nil {
		t.Fatal("/warnings did not open the warnings viewer")
	}
	if view := utils.StripANSI(model.viewString()); !strings.Contains(view, "Warnings \u00b7 1 of 1") || !strings.Contains(view, "Config warning") {
		t.Fatalf("/warnings viewer missing the retained diagnostic:\n%s", view)
	}
	if got := len(model.SubmittedRequests()); got != 0 {
		t.Fatalf("/warnings submitted %d requests, want none", got)
	}
}

// Mirrors Rust's `warning_entries(transcript_cells)`: an ordinary thread warning
// is retained for the viewer, not only the startup summary.
func TestWarningsViewerIncludesThreadWarningsLikeRust(t *testing.T) {
	enabled := true
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, ShowTooltips: &enabled})
	model.Update(StatusMsg{Status: "warning: A runtime warning"})

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyF2})
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "Warnings \u00b7 1 of 1 \u00b7 Warning") {
		t.Fatalf("viewer did not include the thread warning title:\n%s", view)
	}
	if !strings.Contains(view, "A runtime warning") {
		t.Fatalf("viewer did not include the thread warning detail:\n%s", view)
	}
}

// Mirrors Rust's passive warning notice: the composer footer right-aligns the
// retained-warning count with the open-warnings shortcut, and dismissing the
// warning clears it.
func TestWarningsFooterNoticeLikeRust(t *testing.T) {
	enabled := true
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, ShowTooltips: &enabled})
	model.Update(StatusMsg{Status: "warning: A runtime warning"})

	view := utils.StripANSI(model.viewString())
	// The static footer hint is wide, so the notice degrades to its compact form
	// (Rust's `warning_notice` budget rule); the header/history warning lines also
	// start with the same glyph, so match the count form.
	if !hasWarningNotice(view) {
		t.Fatalf("footer warning notice missing:\n%s", view)
	}

	// Dismissing the warning through the viewer clears the notice.
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyF2})
	// The page must be drawn for the dismissal to apply (Rust discards only
	// warnings actually shown).
	if view := utils.StripANSI(model.viewString()); !strings.Contains(view, "A runtime warning") {
		t.Fatalf("viewer did not draw the warning:\n%s", view)
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyEsc})
	if model.warningsView != nil {
		t.Fatal("Esc did not close the viewer")
	}
	if got := model.warningDisplay.WarningCount(); got != 0 {
		t.Fatalf("warning count after dismissal = %d, want 0", got)
	}
	if view := utils.StripANSI(model.viewString()); hasWarningNotice(view) {
		t.Fatalf("dismissed warning still counted in the footer:\n%s", view)
	}
}

func hasWarningNotice(view string) bool {
	return strings.Contains(view, "\u26a0 1 warning") || strings.Contains(view, "\u26a0 1 \u00b7")
}

// Mirrors the popup gates of `show_warning_notice`: an open command popup keeps
// the passive row for itself, so the warning notice yields.
func TestWarningsFooterNoticeYieldsToPopupsLikeRust(t *testing.T) {
	enabled := true
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, ShowTooltips: &enabled})
	model.Update(StatusMsg{Status: "warning: A runtime warning"})
	if view := utils.StripANSI(model.viewString()); !hasWarningNotice(view) {
		t.Fatalf("baseline notice missing:\n%s", view)
	}
	// Open the slash popup by typing `/`.
	typeText(t, model, "/")
	if view := utils.StripANSI(model.viewString()); hasWarningNotice(view) {
		t.Fatalf("notice rendered while the slash popup is open:\n%s", view)
	}
}

func TestWarningsDismissalResetsWithTranscriptLikeRust(t *testing.T) {
	enabled := true
	model := NewModel(codextui.NewState(nil), Options{
		Width:        100,
		Height:       30,
		ShowTooltips: &enabled,
	})
	model.Update(StatusMsg{Status: "warning: repeated diagnostic"})
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyF2})
	model.viewString()
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyEsc})
	if got := model.warningDisplay.WarningCount(); got != 0 {
		t.Fatalf("dismissed warning count = %d, want 0", got)
	}

	model.resetThreadScopedState()
	if len(model.warningCells) != 0 || model.warningsView != nil {
		t.Fatalf("warning state survived transcript reset: cells=%d viewer=%v", len(model.warningCells), model.warningsView)
	}
	model.Update(StatusMsg{Status: "warning: repeated diagnostic"})
	if got := model.warningDisplay.VisibleEntries([]historycell.WarningCell{model.warningCells[0]}); len(got) != 1 {
		t.Fatalf("same warning after reset = %#v, want visible", got)
	}
}
