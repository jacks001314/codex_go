package tea

import (
	"strings"
	"testing"

	"codex_go/tui/bottom_pane"
)

// voiceComposerModel starts a connected voice session on the visible thread
// with a one-line draft so the composer block has a row to protect.
func voiceComposerModel(t *testing.T) *Model {
	t.Helper()
	model, _, _ := newVoiceTestModel(t)
	// Keep the fit threshold independent of the checkout path.
	model.State.CWD = "/repo"
	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	model.composer.SetValue("hello")
	return model
}

func firstRowContaining(view string, needle string) int {
	for index, row := range strings.Split(view, "\n") {
		if strings.Contains(row, needle) {
			return index
		}
	}
	return -1
}

// Rust #43683 renders the voice controls inside the composer, one row below its
// top padding, with a gap row above the draft, while the status line stays
// visible below the composer.
func TestVoiceStripRendersInsideTheComposerLikeRust(t *testing.T) {
	model := voiceComposerModel(t)
	view := plainView(model.viewString())

	stripRow := firstRowContaining(view, "/voice stop")
	if stripRow < 0 {
		t.Fatalf("voice strip missing from the frame:\n%s", view)
	}
	if model.composerTopRow <= 0 {
		t.Fatalf("composer top row = %d", model.composerTopRow)
	}
	if stripRow != model.composerTopRow+1 {
		t.Fatalf("strip row = %d, want the composer's first row after padding (%d):\n%s", stripRow, model.composerTopRow+1, view)
	}
	rows := strings.Split(view, "\n")
	if strings.TrimSpace(rows[stripRow-1]) != "" {
		t.Fatalf("composer padding row above the strip = %q", rows[stripRow-1])
	}
	stripLines := model.voiceStripLines(model.width)
	draftRow := firstRowContaining(view, "hello")
	if draftRow != stripRow+len(stripLines)+1 {
		t.Fatalf("draft row = %d, want the strip (%d rows) plus one gap row after %d:\n%s", draftRow, len(stripLines), stripRow, view)
	}
	if rows[draftRow-1] != strings.Repeat(" ", len(rows[draftRow-1])) {
		t.Fatalf("gap row between the strip and the draft = %q", rows[draftRow-1])
	}
	if strings.TrimSpace(rows[draftRow+1]) != "" {
		t.Fatalf("composer bottom padding row = %q", rows[draftRow+1])
	}
	// The passive rows still follow the composer block. The status row carries the
	// checkout path, so only its position below the composer is asserted.
	statusRow := draftRow + 2
	if statusRow >= len(rows) || strings.TrimSpace(rows[statusRow]) == "" {
		t.Fatalf("status row %d missing below the composer:\n%s", statusRow, view)
	}
}

// Rust #43683 hides the strip when vertical space is needed for the draft and
// cursor; the draft row itself stays visible.
func TestVoiceStripHidesWhenTheComposerIsClippedLikeRust(t *testing.T) {
	model := voiceComposerModel(t)
	stripHeight := bottompane.VoiceStripHeight(model.width, true)
	needed := stripHeight + 4 + model.nonComposerBottomRows()
	if needed < 2 {
		t.Fatalf("strip fit threshold = %d", needed)
	}

	model.applyLayoutSize(model.width, needed)
	if lines := model.voiceStripLines(model.width); len(lines) != stripHeight {
		t.Fatalf("strip at the fit threshold = %#v", lines)
	}
	if view := plainView(model.viewString()); !strings.Contains(view, "/voice stop") {
		t.Fatalf("strip missing at the fit threshold:\n%s", view)
	}

	model.applyLayoutSize(model.width, needed-1)
	if lines := model.voiceStripLines(model.width); lines != nil {
		t.Fatalf("clipped composer kept the strip: %#v", lines)
	}
	view := plainView(model.viewString())
	if strings.Contains(view, "/voice stop") {
		t.Fatalf("clipped composer rendered the strip:\n%s", view)
	}
	if !strings.Contains(view, "hello") {
		t.Fatalf("clipped composer lost the draft:\n%s", view)
	}
	if rows := model.voiceStripRows(model.width); rows != 0 {
		t.Fatalf("clipped composer reserved %d strip rows", rows)
	}
}

// Rust #43683 only paints the strip for the session's own thread.
func TestVoiceStripRequiresTheVisibleThreadLikeRust(t *testing.T) {
	model := voiceComposerModel(t)
	if lines := model.voiceStripLines(model.width); len(lines) == 0 {
		t.Fatal("the visible thread lost its strip")
	}
	model.State.SetThreadID("thread-elsewhere")
	if lines := model.voiceStripLines(model.width); lines != nil {
		t.Fatalf("parked session painted on another thread: %#v", lines)
	}
	if view := model.viewString(); strings.Contains(view, "/voice stop") {
		t.Fatalf("parked session painted on another thread:\n%s", view)
	}
	model.State.SetThreadID("thread-voice")
	if lines := model.voiceStripLines(model.width); len(lines) == 0 {
		t.Fatal("returning to the session's thread did not restore the strip")
	}
}

// Reduced motion keeps the strip inside the composer but reserves only the
// control row plus Rust's gap row.
func TestVoiceStripReducedMotionReservesOneComposerRowLikeRust(t *testing.T) {
	model := voiceComposerModel(t)
	model.animationsEnabled = false
	lines := model.voiceStripLines(model.width)
	if len(lines) != 1 {
		t.Fatalf("reduced-motion strip = %#v", lines)
	}
	if rows := model.voiceStripRows(model.width); rows != 2 {
		t.Fatalf("reduced-motion reserve = %d, want the control row plus its gap", rows)
	}
	if got, want := model.composerSectionRows(), model.composerTargetRows()+2+2; got != want {
		t.Fatalf("composer block = %d, want %d", got, want)
	}
	view := model.viewString()
	stripRow := firstRowContaining(view, "/voice stop")
	if stripRow < 0 || stripRow != model.composerTopRow+1 {
		t.Fatalf("reduced-motion strip row = %d (composer top %d):\n%s", stripRow, model.composerTopRow, view)
	}
	if strings.Contains(view, "codex ") {
		t.Fatalf("reduced motion painted a meter row:\n%s", view)
	}
}
