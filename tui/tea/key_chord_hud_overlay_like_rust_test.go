package tea

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust #59739 ShortcutHud::render_overlay: the guide floats over the frame in
// the bottom-right corner instead of reserving rows, so the composer and the
// status row stay exactly where they were.
func TestKeyChordHUDOverlaysWithoutMovingComposerLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 120, Height: 30})
	now := time.Unix(900, 0)
	model.now = func() time.Time { return now }
	before := model.viewString()

	model.Update(key(keymsg.KeyCtrlX))
	now = now.Add(keyChordRevealDelay)
	after := model.viewString()

	if got, want := strings.Count(after, "\n"), strings.Count(before, "\n"); got != want {
		t.Fatalf("guide changed the frame height: %d rows, want %d", got+1, want+1)
	}
	beforeLines := strings.Split(before, "\n")
	afterLines := strings.Split(after, "\n")
	if beforeLines[len(beforeLines)-1] != afterLines[len(afterLines)-1] {
		t.Fatalf("guide moved the last row:\nbefore %q\nafter  %q", beforeLines[len(beforeLines)-1], afterLines[len(afterLines)-1])
	}

	row := -1
	for index, line := range afterLines {
		if strings.Contains(line, "ctrl+x · Conversation") {
			row = index
			break
		}
	}
	if row < 0 {
		t.Fatalf("guide title missing from the frame:\n%s", after)
	}
	if row == len(afterLines)-1 {
		t.Fatalf("guide painted on the reserved bottom row: row %d of %d", row, len(afterLines))
	}
	// The panel is half the frame wide and right-aligned with a two-column
	// margin, so the title starts past the frame's midpoint.
	column := strings.Index(afterLines[row], "ctrl+x · Conversation")
	if codextui.DisplayWidth(afterLines[row][:column]) < 58 {
		t.Fatalf("guide title starts at column %d, want the right half", codextui.DisplayWidth(afterLines[row][:column]))
	}
	if codextui.DisplayWidth(afterLines[row]) > 120 {
		t.Fatalf("guide row is wider than the frame: %d", codextui.DisplayWidth(afterLines[row]))
	}
}

// A full-screen surface keeps its rows while the guide is painted over it.
func TestKeyChordHUDOverlaysFullScreenSurfaceLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 100, Height: 24})
	now := time.Unix(901, 0)
	model.now = func() time.Time { return now }
	model.Update(key(keymsg.KeyCtrlX))
	now = now.Add(keyChordRevealDelay)

	pad := strings.Repeat("x", 60)
	rows := []string{}
	for index := 1; index <= 8; index++ {
		rows = append(rows, "line-"+string(rune('0'+index))+"-"+pad)
	}
	view := strings.Join(rows, "\n")
	composed := model.composeWithKeyChordHUD(view)
	if got, want := strings.Count(composed, "\n"), strings.Count(view, "\n"); got != want {
		t.Fatalf("overlay changed the surface height: %d rows, want %d", got+1, want+1)
	}
	if !strings.Contains(composed, "ctrl+x · Conversation") {
		t.Fatalf("guide missing from the overlaid surface:\n%s", composed)
	}
	if !strings.Contains(composed, keyChordHUDCancel) {
		t.Fatalf("guide cancellation hint missing:\n%s", composed)
	}
	if !strings.Contains(composed, "line-1") || !strings.Contains(composed, "line-8") {
		t.Fatalf("overlay dropped base rows:\n%s", composed)
	}
}
