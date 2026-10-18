package tea

import (
	"fmt"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func newOwnedComposerModel(t *testing.T, height int) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	for i := 0; i < 40; i++ {
		state.AddMessage(codextui.RoleSystem, fmt.Sprintf("event %02d\nmore detail", i))
	}
	model := NewModel(state, Options{
		Width:               120,
		Height:              height,
		AlternateScreenMode: "always",
	})
	lines := make([]string, 30)
	for i := range lines {
		lines[i] = fmt.Sprintf("draft-row-%02d", i)
	}
	model.composer.SetValue(strings.Join(lines, "\n"))
	model.View()
	return model
}

func composerWheel(button bubbletea.MouseButton, y int) bubbletea.MouseMsg {
	return bubbletea.MouseMsg{
		Action: bubbletea.MouseActionPress,
		Button: button,
		X:      5,
		Y:      y,
	}
}

// TestOwnedComposerCapsLongDraftsLikeRust pins #50109: the fullscreen composer
// grows with the draft but never exceeds two thirds of the screen, and the
// whole section (including its rounded border) respects the cap.
func TestOwnedComposerCapsLongDraftsLikeRust(t *testing.T) {
	model := newOwnedComposerModel(t, 30)
	if got, want := model.composer.Height(), 18; got != want {
		t.Fatalf("composer height = %d, want %d (two thirds of 30 minus the two border rows)", got, want)
	}
	if model.composerTopRow <= 0 {
		t.Fatalf("composer top row = %d, want a painted position below the transcript", model.composerTopRow)
	}
	if got, want := model.composerSectionRows(), 20; got != want {
		t.Fatalf("composer section rows = %d, want %d", got, want)
	}

	short := newOwnedComposerModel(t, 12)
	if got, want := short.composer.Height(), 8; got != want {
		t.Fatalf("small-screen composer height = %d, want the eight-row floor %d", got, want)
	}

	wrapped := newOwnedComposerModel(t, 30)
	wrapped.composer.SetValue(strings.Repeat("wrapped-row ", 400))
	wrapped.View()
	if got, want := wrapped.composer.Height(), 18; got != want {
		t.Fatalf("wrapped composer height = %d, want %d", got, want)
	}
	if wrapped.composerContentRows() <= wrapped.composerTargetRows() {
		t.Fatalf("wrapped draft rows = %d, want more than the %d-row window", wrapped.composerContentRows(), wrapped.composerTargetRows())
	}
}

// TestOwnedComposerWheelBrowsesWithoutMovingCaretOrTranscriptLikeRust pins the
// core interaction from #50109: wheel up over the prompt reveals earlier rows,
// paints the hidden-row arrows, keeps the caret at the draft's end, and leaves
// the transcript viewport alone.
func TestOwnedComposerWheelBrowsesWithoutMovingCaretOrTranscriptLikeRust(t *testing.T) {
	model := newOwnedComposerModel(t, 30)
	transcriptOffset := model.TranscriptYOffset()
	cursorLine := model.composer.Line()
	if transcriptOffset <= 0 {
		t.Fatalf("fixture transcript offset = %d, want a scrollable transcript", transcriptOffset)
	}
	if got, want := model.composerScroll, 12; got != want {
		t.Fatalf("initial composer scroll = %d, want the caret-following bottom %d", got, want)
	}

	model.Update(composerWheel(bubbletea.MouseButtonWheelUp, model.composerTopRow+1))
	if got, want := model.composerScroll, 9; got != want {
		t.Fatalf("composer scroll after wheel up = %d, want %d", got, want)
	}
	if !model.composerManual {
		t.Fatal("wheel browsing must detach from caret following")
	}
	if got := model.TranscriptYOffset(); got != transcriptOffset {
		t.Fatalf("transcript offset after composer wheel = %d, want unchanged %d", got, transcriptOffset)
	}
	if got := model.composer.Line(); got != cursorLine {
		t.Fatalf("composer cursor line after wheel = %d, want unchanged %d", got, cursorLine)
	}

	rendered := model.View()
	if !strings.Contains(rendered, "draft-row-09") {
		t.Fatalf("wheeled-up composer is missing the first visible row:\n%s", rendered)
	}
	if strings.Contains(rendered, "draft-row-29") {
		t.Fatalf("wheeled-up composer still shows the offscreen caret row:\n%s", rendered)
	}
	if !strings.Contains(rendered, "↑") || !strings.Contains(rendered, "↓") {
		t.Fatalf("hidden rows in both directions should paint arrows:\n%s", rendered)
	}

	model.Update(composerWheel(bubbletea.MouseButtonWheelDown, model.composerTopRow+1))
	rendered = model.View()
	if got, want := model.composerScroll, 12; got != want {
		t.Fatalf("composer scroll after wheel down = %d, want %d", got, want)
	}
	if !strings.Contains(rendered, "draft-row-29") {
		t.Fatalf("wheel-down composer is missing the caret row:\n%s", rendered)
	}
	if strings.Contains(rendered, "↓") {
		t.Fatalf("bottom arrow should disappear at the bottom window:\n%s", rendered)
	}
	if !strings.Contains(rendered, "↑") {
		t.Fatalf("hidden earlier rows should keep the top arrow at the bottom window:\n%s", rendered)
	}
}

// TestOwnedComposerWheelDownAtBottomIsConsumedLikeRust keeps the transcript
// still when the pointer is over the prompt even if the prompt cannot move.
func TestOwnedComposerWheelDownAtBottomIsConsumedLikeRust(t *testing.T) {
	model := newOwnedComposerModel(t, 30)
	transcriptOffset := model.TranscriptYOffset()
	model.Update(composerWheel(bubbletea.MouseButtonWheelDown, model.composerTopRow+1))
	if got, want := model.composerScroll, 12; got != want {
		t.Fatalf("composer scroll at bottom = %d, want %d", got, want)
	}
	if got := model.TranscriptYOffset(); got != transcriptOffset {
		t.Fatalf("transcript offset = %d, want unchanged %d", got, transcriptOffset)
	}
}

// TestOwnedComposerResizeClampsScrollLikeRust keeps manual browsing anchored
// while the available window changes at the scroll boundaries.
func TestOwnedComposerResizeClampsScrollLikeRust(t *testing.T) {
	model := newOwnedComposerModel(t, 30)
	for i := 0; i < 10; i++ {
		model.Update(composerWheel(bubbletea.MouseButtonWheelUp, model.composerTopRow+1))
	}
	model.View()
	if got, want := model.composerScroll, 0; got != want {
		t.Fatalf("top scroll = %d, want %d", got, want)
	}
	model.resize(120, 20)
	model.View()
	if got, want := model.composer.Height(), 11; got != want {
		t.Fatalf("resized composer height = %d, want %d", got, want)
	}
	if got, want := model.composerScroll, 0; got != want {
		t.Fatalf("top scroll after shrink = %d, want %d", got, want)
	}

	for i := 0; i < 20; i++ {
		model.Update(composerWheel(bubbletea.MouseButtonWheelDown, model.composerTopRow+1))
	}
	model.View()
	if got, want := model.composerScroll, 19; got != want {
		t.Fatalf("bottom scroll at 20 rows = %d, want %d", got, want)
	}
	model.resize(120, 40)
	model.View()
	if got, want := model.composer.Height(), 24; got != want {
		t.Fatalf("grown composer height = %d, want %d", got, want)
	}
	if got, want := model.composerScroll, 6; got != want {
		t.Fatalf("scroll after growth = %d, want clamped %d", got, want)
	}
}

// TestOwnedComposerEditingResumesCaretFollowingLikeRust covers key input and
// external draft edits after manual wheel browsing.
func TestOwnedComposerEditingResumesCaretFollowingLikeRust(t *testing.T) {
	model := newOwnedComposerModel(t, 30)
	for i := 0; i < 10; i++ {
		model.Update(composerWheel(bubbletea.MouseButtonWheelUp, model.composerTopRow+1))
	}
	if got, want := model.composerScroll, 0; got != want {
		t.Fatalf("composer scroll at top = %d, want %d", got, want)
	}
	model.View()

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("x")})
	if model.composerManual {
		t.Fatal("editor input must resume caret following")
	}
	model.View()
	if got, want := model.composerScroll, 12; got != want {
		t.Fatalf("composer scroll after input = %d, want caret-following %d", got, want)
	}

	model.Update(composerWheel(bubbletea.MouseButtonWheelUp, model.composerTopRow+1))
	model.composer.SetValue("external edit\n" + strings.Repeat("row\n", 40))
	model.View()
	if model.composerManual {
		t.Fatal("an external draft edit must resume caret following")
	}
}

// TestOwnedComposerTranscriptWheelStillScrollsTranscript keeps the transcript
// surface live when the pointer is not over the prompt.
func TestOwnedComposerTranscriptWheelStillScrollsTranscript(t *testing.T) {
	model := newOwnedComposerModel(t, 30)
	before := model.TranscriptYOffset()
	model.Update(composerWheel(bubbletea.MouseButtonWheelUp, 1))
	if got, want := model.TranscriptYOffset(), before-1; got != want {
		t.Fatalf("transcript offset = %d, want %d", got, want)
	}
	if model.composerManual {
		t.Fatal("a transcript wheel must not detach the composer caret")
	}
}

// TestInlineComposerKeepsFixedHeightLikeRustScope ensures the owned-screen
// changes do not alter the inline layout.
func TestInlineComposerKeepsFixedHeightLikeRustScope(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 80, Height: 30})
	model.composer.SetValue(strings.Repeat("inline row\n", 40))
	model.View()
	if got, want := model.composer.Height(), defaultComposerHeight; got != want {
		t.Fatalf("inline composer height = %d, want the fixed %d", got, want)
	}
}
